// SPDX-License-Identifier: Apache-2.0
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWalk_FindsAllScenarios(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "llm", "ok.yaml"), `
name: llm-ok
description: ok scenario
experiments:
  - name: e
    fault: {http_status: 500}
    match: {host: a}
    probability: 0.1
`)
	mustWrite(t, filepath.Join(dir, "backend", "pack-style", "scenario.yaml"), `
name: pack-ok
experiments:
  - name: e
    fault: {errno: EIO}
    match: {syscall: read}
    probability: 0.1
`)
	// Non-YAML file should be ignored.
	mustWrite(t, filepath.Join(dir, "llm", "README.md"), "noise")

	entries, err := walk(dir)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	// Sorted by pack, then name. backend/pack-ok comes before llm/llm-ok.
	if entries[0].Name != "pack-ok" || entries[1].Name != "llm-ok" {
		t.Errorf("sort order wrong: %+v", entries)
	}
	if entries[0].Mode != "ebpf" {
		t.Errorf("pack-ok mode = %q, want ebpf", entries[0].Mode)
	}
	if entries[1].Mode != "proxy" {
		t.Errorf("llm-ok mode = %q, want proxy", entries[1].Mode)
	}
}

func TestWalk_InvalidScenarioIsError(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "bad.yaml"), "not: : valid:::\n  - syntax")
	_, err := walk(dir)
	if err == nil {
		t.Fatalf("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("err should mention bad.yaml: %v", err)
	}
}

func TestRender_GroupsByPackWithCorrectPlatform(t *testing.T) {
	entries := []entry{
		{Pack: "llm", Name: "a", Mode: "proxy", Platform: "any", Description: "a"},
		{Pack: "backend", Name: "b", Mode: "ebpf", Platform: "linux", Description: "b"},
	}
	out, err := render(entries)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"# Scenario registry",
		"Auto-generated catalog",
		"## LLM and gateway",
		"## Backend classics",
		"| `a` | proxy | any | a |",
		"| `b` | ebpf | linux | b |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPackTitle(t *testing.T) {
	cases := map[string]string{
		"llm":        "LLM and gateway",
		"rag":        "RAG and vector DB",
		"tool-calls": "Tool calls and subprocesses",
		"backend":    "Backend classics",
		"custom":     "Community contributions",
		"unknown":    "Unknown",
	}
	for pack, want := range cases {
		if got := packTitle(pack); got != want {
			t.Errorf("packTitle(%q) = %q, want %q", pack, got, want)
		}
	}
}

func TestModeOf(t *testing.T) {
	// Use a tiny Scenario through the loader to drive modeOf.
	dir := t.TempDir()
	httpPath := filepath.Join(dir, "http.yaml")
	mustWrite(t, httpPath, `
name: http-test
experiments:
  - name: e
    fault: {http_status: 500}
    match: {host: a}
    probability: 0.1
`)
	// We don't have a scenario.Load helper in this package; reach into
	// the same loader that walk uses. Skip the test rather than pull
	// in a heavier helper.
	t.Skip("modeOf is exercised end-to-end by TestWalk_FindsAllScenarios")
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
