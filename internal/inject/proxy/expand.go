package proxy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
	"github.com/faultkit/faultkit/pkg/faulttypes"
	"github.com/faultkit/faultkit/pkg/scenario"
)

var (
	errUnknownFailureMode = errors.New("unknown failure mode")
	errUnknownProvider    = errors.New("unknown provider")
)

// expandScenario resolves fixture-driven experiments (those naming a failure
// mode) into concrete host/path/fault experiments via the fixture catalog,
// fanning out across providers. Raw experiments (explicit fault+match) pass
// through unchanged, so the matcher and faulter downstream always see a flat
// list of fully-specified experiments.
//
// providerScope, when non-empty (the --provider flag), restricts fixture
// fan-out to that single provider for experiments that don't already name one.
// An experiment's own Provider field takes precedence over providerScope.
func expandScenario(s *scenario.Scenario, providerScope string) (*scenario.Scenario, error) {
	if s == nil {
		return nil, errors.New("proxy: nil scenario")
	}
	out := make([]scenario.Experiment, 0, len(s.Experiments))
	for _, exp := range s.Experiments {
		if !exp.IsFixtureDriven() {
			out = append(out, exp)
			continue
		}
		if !fixtures.KnownMode(exp.Failure) {
			return nil, fmt.Errorf("%w %q (known: %s)", errUnknownFailureMode, exp.Failure, strings.Join(fixtures.Modes(), ", "))
		}
		scope := exp.Provider
		if scope == "" {
			scope = providerScope
		}
		providers, err := providersForMode(exp.Failure, scope)
		if err != nil {
			return nil, err
		}
		for _, p := range providers {
			f, _ := fixtures.For(exp.Failure, p.id)
			out = append(out, scenario.Experiment{
				Name:        exp.Name + "-" + p.id,
				Fault:       fixtureFault(f),
				Match:       scenario.Match{Host: p.upstream, Path: f.Path},
				Probability: exp.Probability,
			})
		}
	}
	expanded := *s
	expanded.Experiments = out
	return &expanded, nil
}

// providersForMode returns the providers a fixture-driven experiment fans out
// to. scope=="" means every registered provider with a fixture for mode;
// otherwise the single named provider, which must exist and have a fixture.
func providersForMode(mode, scope string) ([]provider, error) {
	if scope != "" {
		p, ok := providerByID(scope)
		if !ok {
			return nil, fmt.Errorf("%w %q (known: %s)", errUnknownProvider, scope, strings.Join(ProviderIDs(), ", "))
		}
		if _, ok := fixtures.For(mode, scope); !ok {
			return nil, fmt.Errorf("failure mode %q has no fixture for provider %q", mode, scope)
		}
		return []provider{p}, nil
	}
	var out []provider
	for _, p := range providerRegistry {
		if _, ok := fixtures.For(mode, p.id); ok {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("failure mode %q has no fixtures for any provider", mode)
	}
	return out, nil
}

func fixtureFault(f fixtures.Fixture) faulttypes.Fault {
	return faulttypes.Fault{
		HTTPStatus:         f.Status,
		ResponseHeaders:    f.Headers,
		ResponseBody:       f.Body,
		StreamCutoffTokens: f.StreamCutoffTokens,
	}
}
