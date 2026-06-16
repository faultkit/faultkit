package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/faultkit/faultkit/internal/inject"
	"github.com/faultkit/faultkit/internal/inject/ebpf"
	"github.com/faultkit/faultkit/internal/inject/proxy"
	"github.com/faultkit/faultkit/internal/report"
	"github.com/faultkit/faultkit/internal/runner"
	"github.com/faultkit/faultkit/pkg/scenario"
)

const (
	modeAuto  = "auto"
	modeProxy = "proxy"
	modeEBPF  = "ebpf"
)

func newRunCmd() *cobra.Command {
	var opts runOpts
	cmd := &cobra.Command{
		Use:   "run [flags] -- <target> [target args...]",
		Short: "Run a target command under fault injection",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.target = args
			opts.stdout = cmd.OutOrStdout()
			opts.stderr = cmd.ErrOrStderr()
			return runFaultkit(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.scenarioName, "scenario", "", "builtin scenario name")
	cmd.Flags().StringVar(&opts.configPath, "config", "", "scenario YAML file")
	cmd.Flags().StringVar(&opts.mode, "mode", modeAuto, "injection mode: auto, proxy, ebpf")
	cmd.Flags().StringVar(&opts.reportPath, "report", "", "write JSON report to path")
	cmd.Flags().BoolVar(&opts.verbose, "verbose", false, "log injector activity and each fault as it fires")
	cmd.Flags().BoolVar(&opts.baseURL, "base-url", false, "point the target's SDK at faultkit via *_BASE_URL env (OPENAI_BASE_URL, ANTHROPIC_BASE_URL) instead of HTTPS_PROXY — for clients that ignore proxy env")
	return cmd
}

type runOpts struct {
	scenarioName, configPath, mode, reportPath string
	verbose, baseURL                           bool
	target                                     []string
	stdout, stderr                             io.Writer
}

func runFaultkit(parentCtx context.Context, o runOpts) error {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, stopSignals := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	var vlog inject.Logf
	if o.verbose {
		vlog = func(format string, args ...any) {
			fmt.Fprintf(o.stderr, "[faultkit] "+format+"\n", args...)
		}
	}

	s, err := loadScenario(o)
	if err != nil {
		return err
	}
	if len(o.target) == 0 {
		return UsageErrorf("missing target command after --")
	}

	inj, err := pickInjector(s, o.mode, o.baseURL)
	if err != nil {
		return err
	}
	if vlog != nil {
		if va, ok := inj.(inject.VerboseAware); ok {
			va.SetVerbose(vlog)
		}
		effMode := o.mode
		if effMode == modeAuto {
			effMode = scenarioMode(s)
		}
		vlog("mode=%s scenario=%s", effMode, s.Name)
	}

	env, err := inj.Start(ctx, s)
	if err != nil {
		return fmt.Errorf("starting injector: %w", err)
	}

	// Drain concurrently so the bounded events channel never backs
	// up and drops on long runs.
	drainDone := make(chan []inject.Event, 1)
	go func() {
		drainDone <- drainEvents(inj.Events())
	}()

	r := &runner.Runner{
		Stdout: o.stdout,
		Stderr: o.stderr,
		OnStarted: func(pid int) {
			pa, ok := inj.(inject.PIDAware)
			if !ok {
				return
			}
			if err := pa.SetTargetPID(pid); err != nil {
				fmt.Fprintf(o.stderr, "warning: injector pid configure: %v\n", err)
			}
		},
	}
	start := time.Now()
	result, runErr := r.Run(ctx, o.target, env)
	duration := time.Since(start)

	if stopErr := inj.Stop(ctx); stopErr != nil {
		fmt.Fprintf(o.stderr, "warning: injector stop: %v\n", stopErr)
	}

	events := <-drainDone

	if vlog != nil {
		for _, ev := range events {
			vlog("%s", verboseEventLine(ev))
		}
	}

	targetExit := 0
	if result != nil {
		targetExit = result.ExitCode
	}
	summary := report.Summary{
		Scenario:   s.Name,
		Target:     o.target,
		Duration:   duration,
		TargetExit: targetExit,
		Events:     events,
	}
	_ = report.WriteTerminal(o.stderr, summary)
	if o.reportPath != "" {
		if err := writeJSONReport(o.reportPath, summary); err != nil {
			fmt.Fprintf(o.stderr, "warning: writing report: %v\n", err)
		}
	}

	if runErr != nil {
		return runErr
	}
	if targetExit != 0 {
		return &runner.TargetExitError{ExitCode: targetExit}
	}
	if summary.FiredCount() == 0 {
		if ro, ok := inj.(inject.RequestObserver); ok && ro.RequestsSeen() == 0 {
			warnNoTrafficReached(o)
		}
		return runner.ErrFaultNotFired
	}
	return nil
}

// warnNoTrafficReached flags the silent-green trap: the run "passed" only
// because no request ever reached faultkit, so no fault could fire. The
// likely cause differs by mode.
func warnNoTrafficReached(o runOpts) {
	if o.baseURL {
		fmt.Fprintln(o.stderr, "warning: no requests reached faultkit — the target's SDK did not use the "+
			"injected base URL (OPENAI_BASE_URL / ANTHROPIC_BASE_URL). Confirm the SDK reads it and the "+
			"scenario host is a supported provider.")
		return
	}
	fmt.Fprintln(o.stderr, "warning: no requests reached faultkit's proxy — the target likely ignores "+
		"HTTPS_PROXY (common with Node fetch/undici and subprocess SDKs). For SDK clients, try --base-url.")
}

func loadScenario(o runOpts) (*scenario.Scenario, error) {
	switch {
	case o.scenarioName != "" && o.configPath != "":
		return nil, UsageErrorf("--scenario and --config are mutually exclusive")
	case o.scenarioName != "":
		s, err := scenario.LoadBuiltin(o.scenarioName)
		if err != nil {
			return nil, UsageErrorf("%w", err)
		}
		return s, nil
	case o.configPath != "":
		s, err := scenario.Load(o.configPath)
		if err != nil {
			return nil, UsageErrorf("%w", err)
		}
		return s, nil
	default:
		return nil, UsageErrorf("either --scenario or --config is required")
	}
}

// scenarioMode returns the injection mode a scenario maps to in --auto:
// modeProxy for any HTTP experiment, modeEBPF for syscall-only, or ""
// when the scenario has no experiments any injector can serve.
func scenarioMode(s *scenario.Scenario) string {
	hasHTTP, hasSyscall := experimentKinds(s)
	switch {
	case hasHTTP:
		return modeProxy
	case hasSyscall:
		return modeEBPF
	}
	return ""
}

func experimentKinds(s *scenario.Scenario) (hasHTTP, hasSyscall bool) {
	for _, exp := range s.Experiments {
		if exp.Match.IsHTTP() {
			hasHTTP = true
		}
		if exp.Match.IsSyscall() {
			hasSyscall = true
		}
	}
	return hasHTTP, hasSyscall
}

func pickInjector(s *scenario.Scenario, mode string, baseURL bool) (inject.Injector, error) {
	hasHTTP, hasSyscall := experimentKinds(s)
	switch mode {
	case modeAuto:
		switch {
		case hasHTTP:
			return newProxyInjector(baseURL), nil
		case hasSyscall:
			if baseURL {
				return nil, errBaseURLNeedsHTTP
			}
			if err := ebpfUnavailableErr(); err != nil {
				return nil, err
			}
			return ebpf.New(), nil
		}
		return nil, fmt.Errorf("scenario %q has no experiments any injector can serve", s.Name)
	case modeProxy:
		if !hasHTTP {
			return nil, UsageErrorf("scenario %q has no HTTP experiments; --mode=proxy is incompatible", s.Name)
		}
		return newProxyInjector(baseURL), nil
	case modeEBPF:
		if baseURL {
			return nil, errBaseURLNeedsHTTP
		}
		if !hasSyscall {
			return nil, UsageErrorf("scenario %q has no syscall experiments; --mode=ebpf is incompatible", s.Name)
		}
		if err := ebpfUnavailableErr(); err != nil {
			return nil, err
		}
		return ebpf.New(), nil
	default:
		return nil, UsageErrorf("--mode must be %s, %s, or %s; got %q", modeAuto, modeProxy, modeEBPF, mode)
	}
}

// errBaseURLNeedsHTTP is returned when --base-url is combined with a
// syscall-only (eBPF) scenario or --mode=ebpf; base-URL injection only
// applies to HTTP/proxy traffic.
var errBaseURLNeedsHTTP = UsageErrorf("--base-url only applies to HTTP scenarios (proxy mode)")

// newProxyInjector builds the proxy injector, switching it into base-URL
// mode when requested.
func newProxyInjector(baseURL bool) inject.Injector {
	p := proxy.New()
	if baseURL {
		p.UseBaseURL(true)
	}
	return p
}

// ebpfUnavailableErr reports a clear error when eBPF mode can't run on this
// host, reusing the same reason `faultkit check` computes. Returning it from
// pickInjector lets `run` fail fast with that reason instead of an opaque
// load/attach failure deep inside the injector's Start.
func ebpfUnavailableErr() error {
	for _, r := range inject.AvailableModes() {
		if r.Mode == inject.ModeEBPF && !r.Available {
			return fmt.Errorf("eBPF mode is not available on this host: %s", r.Reason)
		}
	}
	return nil
}

// verboseEventLine renders one fault event for --verbose output, picking
// the HTTP or syscall fields depending on which injector produced it.
func verboseEventLine(ev inject.Event) string {
	target := fmt.Sprintf("host=%s", ev.Host)
	if ev.Path != "" {
		target = fmt.Sprintf("host=%s path=%s", ev.Host, ev.Path)
	}
	if ev.Syscall != "" {
		target = fmt.Sprintf("syscall=%s pid=%d", ev.Syscall, ev.PID)
	}
	verb := "fault fired"
	if !ev.Fired {
		verb = "fault event"
	}
	line := fmt.Sprintf("%s: experiment=%q %s", verb, ev.Experiment, target)
	if ev.Err != "" {
		line += fmt.Sprintf(" err=%q", ev.Err)
	}
	return line
}

func drainEvents(ch <-chan inject.Event) []inject.Event {
	if ch == nil {
		return nil
	}
	out := []inject.Event{}
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func writeJSONReport(path string, s report.Summary) error {
	f, err := os.Create(path) // #nosec G304 -- caller-provided output path; the contract is "write the report here".
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	defer f.Close()
	return report.WriteJSON(f, s)
}
