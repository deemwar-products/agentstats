// Command analyzer is the agentstats counter/renderer. Two modes:
//
//	analyzer local  --repos '<glob of *.git>' --emails a@x,b@y --agent-start 2025-01 [--login me] [--out dir] [--no-agents]
//	analyzer serve  [--addr :8080]     # HTTP service for the Cloudflare Container (DO-only)
//
// `local` bare-reads existing clones for testing/regression. `serve` bare-clones
// the user's repos with a GitHub App user token (no working-tree checkout),
// runs the counting rules, renders every card/badge, and (TODO) uploads them to
// the R2 bucket over the S3 API. It NEVER writes code or tokens to disk beyond
// the job's scratch dir, which is wiped at the end.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/render"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "local":
		runLocal(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: analyzer local --repos '<glob>' --emails a,b --agent-start 2025-01 [--login me] [--out dir] [--no-agents]")
	fmt.Fprintln(os.Stderr, "       analyzer serve [--addr :8080]")
	os.Exit(2)
}

func runLocal(args []string) {
	var reposGlob, emailsCSV, agentStart, login, out string
	noAgents := false
	agentStart = "2025-01"
	login = "user"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repos":
			reposGlob, i = next(args, i)
		case "--emails":
			emailsCSV, i = next(args, i)
		case "--agent-start":
			agentStart, i = next(args, i)
		case "--login":
			login, i = next(args, i)
		case "--out":
			out, i = next(args, i)
		case "--no-agents":
			noAgents = true
		default:
			fmt.Fprintln(os.Stderr, "unknown flag:", args[i])
			usage()
		}
	}
	if reposGlob == "" || emailsCSV == "" {
		usage()
	}

	repos := globRepos(reposGlob)
	if len(repos) == 0 {
		fmt.Fprintln(os.Stderr, "no repos matched:", reposGlob)
		os.Exit(1)
	}

	a := &analyzer.Analyzer{
		Repos:      repos,
		Emails:     emailSet(emailsCSV),
		AgentStart: agentStart,
	}
	if !noAgents {
		a.AgentRules = analyzer.DefaultAgentRules()
	}

	stats, err := a.Run(login, func(repo string, done, total int) {
		if done%50 == 0 || done == total {
			fmt.Fprintf(os.Stderr, "\r%d/%d repos", done, total)
		}
	})
	fmt.Fprintln(os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if out == "" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(stats)
		return
	}
	if err := writeArtifacts(out, stats); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "wrote stats + cards to", out)
}

// writeArtifacts writes the stats JSON and renders both cards + badge (SVG) to
// a local directory — the same files the container uploads to R2.
func writeArtifacts(dir string, s *analyzer.Stats) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "stats.json"), b, 0o644); err != nil {
		return err
	}
	files := map[string]string{
		"badge.svg":                 render.Badge(s, render.Light),
		"before-after.svg":          render.BeforeAfter(s, render.Light),
		"before-after.dark.svg":     render.BeforeAfter(s, render.Dark),
		"composition.svg":           render.Composition(s, render.Light),
		"composition.dark.svg":      render.Composition(s, render.Dark),
	}
	for name, svg := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(svg), 0o644); err != nil {
			return err
		}
		// PNG next to each SVG when resvg is available (best-effort locally).
		if png, err := render.PNG([]byte(svg)); err == nil {
			pngName := strings.TrimSuffix(name, ".svg") + ".png"
			_ = os.WriteFile(filepath.Join(dir, pngName), png, 0o644)
		}
	}
	return nil
}

func next(args []string, i int) (string, int) {
	if i+1 >= len(args) {
		usage()
	}
	return args[i+1], i + 1
}

func emailSet(csv string) map[string]bool {
	m := map[string]bool{}
	for _, e := range strings.Split(csv, ",") {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			m[e] = true
		}
	}
	return m
}

// globRepos expands a glob of bare repos into []Repo, tagging gitlab paths.
func globRepos(pattern string) []analyzer.Repo {
	matches, _ := filepath.Glob(expandHome(pattern))
	sort.Strings(matches) // deterministic order
	repos := make([]analyzer.Repo, 0, len(matches))
	for _, m := range matches {
		src := "github"
		if strings.Contains(m, "/gitlab/") {
			src = "gitlab"
		}
		repos = append(repos, analyzer.Repo{GitDir: m, Name: filepath.Base(m), Source: src})
	}
	return repos
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}
