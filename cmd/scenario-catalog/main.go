// SPDX-License-Identifier: Apache-2.0
// scenario-catalog walks a faultkit scenario registry root and writes an
// INDEX.md grouped by pack. It is the same code path the registry's
// CI uses; humans can also run it locally before opening a PR.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/faultkit/faultkit/pkg/scenario"
)

type entry struct {
	Pack        string
	Name        string
	Path        string // relative to root
	Mode        string // "proxy" | "ebpf" | "?"
	Platform    string // "linux" | "any"
	Description string
}

type byPack []entry

func (e byPack) Len() int      { return len(e) }
func (e byPack) Swap(i, j int) { e[i], e[j] = e[j], e[i] }
func (e byPack) Less(i, j int) bool {
	if e[i].Pack != e[j].Pack {
		return e[i].Pack < e[j].Pack
	}
	return e[i].Name < e[j].Name
}

func modeOf(s *scenario.Scenario) string {
	hasHTTP, hasSyscall := false, false
	for _, exp := range s.Experiments {
		if exp.Match.IsHTTP() {
			hasHTTP = true
		}
		if exp.Match.IsSyscall() {
			hasSyscall = true
		}
	}
	switch {
	case hasHTTP:
		return "proxy"
	case hasSyscall:
		return "ebpf"
	}
	return "?"
}

func walk(root string) ([]entry, error) {
	var out []entry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base != "scenario.yaml" && !strings.HasSuffix(base, ".yaml") {
			return nil
		}
		s, lerr := scenario.Load(path)
		if lerr != nil {
			return fmt.Errorf("invalid scenario %s: %w", path, lerr)
		}
		rel, lerr := filepath.Rel(root, path)
		if lerr != nil {
			return lerr
		}
		pack := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		mode := modeOf(s)
		out = append(out, entry{
			Pack:        pack,
			Name:        s.Name,
			Path:        rel,
			Mode:        mode,
			Platform:    platformOf(entry{Mode: mode}),
			Description: s.Description,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Sort(byPack(out))
	return out, nil
}

const indexTmpl = `
## {{.PackTitle}}

| Scenario | Mechanism | Platform | Description |
|---|---|---|---|
{{- range .Entries }}
| ` + "`" + `{{.Name}}` + "`" + ` | {{.Mode}} | {{.Platform}} | {{.Description}} |
{{- end }}
`

type renderData struct {
	PackTitle string
	Entries   []entry
}

// packTitle turns a directory name into a human-readable heading.
func packTitle(pack string) string {
	title := map[string]string{
		"llm":        "LLM and gateway",
		"rag":        "RAG and vector DB",
		"tool-calls": "Tool calls and subprocesses",
		"backend":    "Backend classics",
		"custom":     "Community contributions",
	}
	if t, ok := title[pack]; ok {
		return t
	}
	return strings.ToUpper(pack[:1]) + pack[1:]
}

func platformOf(e entry) string {
	if e.Mode == "ebpf" {
		return "linux"
	}
	return "any"
}

func render(entries []entry) (string, error) {
	byPack := map[string][]entry{}
	for _, e := range entries {
		byPack[e.Pack] = append(byPack[e.Pack], e)
	}
	var packs []string
	for p := range byPack {
		packs = append(packs, p)
	}
	sort.Strings(packs)

	var b strings.Builder
	b.WriteString("# Scenario registry\n\n")
	b.WriteString("Auto-generated catalog. Do not edit by hand.\n\n")
	for _, p := range packs {
		rows := byPack[p]
		data := renderData{PackTitle: packTitle(p), Entries: rows}
		t, err := template.New("index").Parse(indexTmpl)
		if err != nil {
			return "", err
		}
		if err := t.Execute(&b, data); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func main() {
	root := flag.String("root", "scenarios", "scenario registry root directory")
	out := flag.String("out", "scenarios/INDEX.md", "output path for the generated catalog")
	flag.Parse()

	entries, err := walk(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	rendered, err := render(entries)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	// #nosec G306 -- catalog output is world-readable markdown; 0o644 is intentional.
	if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d scenarios)\n", *out, len(entries))
}
