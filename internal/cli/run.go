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

	"github.com/faultkit-dev/faultkit/internal/inject"
	"github.com/faultkit-dev/faultkit/internal/inject/ebpf"
	"github.com/faultkit-dev/faultkit/internal/inject/proxy"
	"github.com/faultkit-dev/faultkit/internal/report"
	"github.com/faultkit-dev/faultkit/internal/runner"
	"github.com/faultkit-dev/faultkit/pkg/scenario"
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
	cmd.Flags().BoolVar(&opts.verbose, "verbose", false, "log every fault decision")
	return cmd
}

type runOpts struct {
	scenarioName, configPath, mode, reportPath string
	verbose                                    bool
	target                                     []string
	stdout, stderr                             io.Writer
}

func runFaultkit(parentCtx context.Context, o runOpts) error {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, stopSignals := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	s, err := loadScenario(o)
	if err != nil {
		return err
	}
	if len(o.target) == 0 {
		return UsageErrorf("missing target command after --")
	}

	inj, err := pickInjector(s, o.mode)
	if err != nil {
		return err
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
		return runner.ErrFaultNotFired
	}
	return nil
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

func pickInjector(s *scenario.Scenario, mode string) (inject.Injector, error) {
	hasHTTP, hasSyscall := experimentKinds(s)
	switch mode {
	case modeAuto:
		switch {
		case hasHTTP:
			return proxy.New(), nil
		case hasSyscall:
			return ebpf.New(), nil
		}
		return nil, fmt.Errorf("scenario %q has no experiments any injector can serve", s.Name)
	case modeProxy:
		if !hasHTTP {
			return nil, UsageErrorf("scenario %q has no HTTP experiments; --mode=proxy is incompatible", s.Name)
		}
		return proxy.New(), nil
	case modeEBPF:
		if !hasSyscall {
			return nil, UsageErrorf("scenario %q has no syscall experiments; --mode=ebpf is incompatible", s.Name)
		}
		return ebpf.New(), nil
	default:
		return nil, UsageErrorf("--mode must be %s, %s, or %s; got %q", modeAuto, modeProxy, modeEBPF, mode)
	}
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
