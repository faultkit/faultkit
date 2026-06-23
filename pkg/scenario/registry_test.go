// SPDX-License-Identifier: Apache-2.0
package scenario_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/pkg/scenario"

	_ "github.com/faultkit/faultkit/internal/scenario/builtin"
)

const resolverSingleFileYAML = `
name: reg-test
description: A registry scenario
experiments:
  - name: a
    fault:
      http_status: 500
    match:
      host: example.test
    probability: 0.1
`

const resolverPackStyleYAML = `
name: pack-style
description: A pack-style registry scenario
experiments:
  - name: a
    fault:
      http_status: 502
    match:
      host: example.test
    probability: 0.2
`

func writeResolverFixture(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func TestResolver_PathIsAlwaysUsedAsIs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abs.yaml")
	if err := os.WriteFile(path, []byte(resolverSingleFileYAML), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := scenario.NewResolver("")
	s, err := r.Resolve(path)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if s.Name != "reg-test" {
		t.Errorf("Name = %q, want reg-test", s.Name)
	}
}

func TestResolver_BuiltinWinsOverRegistry(t *testing.T) {
	dir := t.TempDir()
	// Shadow a builtin name with a registry file. Builtin must still win.
	writeResolverFixture(t, dir, "llm-api-degraded.yaml", resolverSingleFileYAML)
	r := scenario.NewResolver(dir)
	s, err := r.Resolve("llm-api-degraded")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if s.Name != "llm-api-degraded" {
		t.Errorf("Name = %q, want llm-api-degraded", s.Name)
	}
}

func TestResolver_SingleFileRegistryHit(t *testing.T) {
	dir := t.TempDir()
	writeResolverFixture(t, dir, "llm/my-scenario.yaml", resolverSingleFileYAML)
	r := scenario.NewResolver(dir)
	s, err := r.Resolve("llm/my-scenario")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if s.Name != "reg-test" {
		t.Errorf("Name = %q, want reg-test", s.Name)
	}
}

func TestResolver_PackStyleRegistryHit(t *testing.T) {
	dir := t.TempDir()
	writeResolverFixture(t, dir, "llm/pack-style/scenario.yaml", resolverPackStyleYAML)
	r := scenario.NewResolver(dir)
	s, err := r.Resolve("llm/pack-style")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if s.Name != "pack-style" {
		t.Errorf("Name = %q, want pack-style", s.Name)
	}
}

func TestResolver_SingleFileWinsOverPackStyle(t *testing.T) {
	// When both <name>.yaml and <name>/scenario.yaml exist, the
	// single-file shape wins. This keeps the user's most common
	// authoring path predictable.
	dir := t.TempDir()
	writeResolverFixture(t, dir, "llm/dupe.yaml", resolverSingleFileYAML)
	writeResolverFixture(t, dir, "llm/dupe/scenario.yaml", resolverPackStyleYAML)
	r := scenario.NewResolver(dir)
	s, err := r.Resolve("llm/dupe")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if s.Name != "reg-test" {
		t.Errorf("Name = %q, want reg-test (single-file should win over pack-style)", s.Name)
	}
}

func TestResolver_NoRegistryRootFallsBackToBuiltin(t *testing.T) {
	dir := t.TempDir()
	writeResolverFixture(t, dir, "llm/only-here.yaml", resolverSingleFileYAML)
	r := scenario.NewResolver("")
	_, err := r.Resolve("llm/only-here")
	if err == nil {
		t.Fatalf("expected miss when --registry-root is empty")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Errorf("err %q should mention the missing registry root", err.Error())
	}
}

func TestResolver_UnknownNameInRegistryReturnsUsageError(t *testing.T) {
	dir := t.TempDir()
	r := scenario.NewResolver(dir)
	_, err := r.Resolve("llm/does-not-exist")
	if err == nil {
		t.Fatalf("expected miss")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("err %q should say 'not found'", err.Error())
	}
}
