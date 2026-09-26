// Package jobs is the in-process analysis queue that replaces the Durable
// Object (doc 12) and makes it visible/fair/cost-capped (doc 13). The pipeline
// wires the existing analyzer + render packages to the store and R2.
package jobs

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/r2"
	"github.com/deemwar-products/agentstats/analyzer/render"
	"github.com/deemwar-products/agentstats/analyzer/store"
)

// JobCloneWorkers is how many repos one job clones/fetches at once.
const JobCloneWorkers = 8

// RepoSpec is one repository to analyze. For the real path CloneURL is set and
// the Cloner produces GitDir; for local dev GitDir is provided directly.
type RepoSpec struct {
	FullName string
	CloneURL string
	Private  bool
	PushedAt string // GitHub pack pushed_at — drives incremental refresh (doc 13)
	GitDir   string // bare clone dir (filled by the Cloner, or preset locally)
}

// Spec is everything one analysis job needs. Token/Emails live only here in
// process memory for the job and are never persisted (docs 05, 07).
type Spec struct {
	GithubID   int64
	Login      string
	Emails     []string
	AgentStart string
	Token      string // user token — in-memory only
	Repos      []RepoSpec
	Refresh    bool          // true when this is a re-analysis (incremental)
	Live       *LiveProgress // in-memory progress for the status page (nil-safe)
}

// Result is the aggregate outcome recorded on the job row.
type Result struct {
	Stats   *analyzer.Stats
	Repos   int
	Commits int
	Seconds int
}

// Cloner makes a repo's bare clone available on disk. GitCloner is the real
// implementation; tests inject a fake. It must never write the token to disk or
// a log (docs 05): the token goes to git via an in-memory http.extraHeader.
type Cloner interface {
	// Ensure returns the bare-clone dir and current HEAD sha for spec, using
	// cacheDir as a per-user persistent clone cache when available. skipped is
	// true when an unchanged repo's cached clone was reused with no network.
	Ensure(spec RepoSpec, cacheDir, token string) (gitDir, headSHA string, skipped bool, err error)
}

// Pipeline runs one job end to end.
type Pipeline struct {
	Store   store.Store
	R2      *r2.Client
	Cloner  Cloner
	Cache   string                 // root dir for per-user clone caches (empty => a fresh temp dir per job)
	Version func(login string) int // returns the next R2 version for a user
}

// Run clones/fetches, analyzes, renders, uploads, and persists aggregates.
// progress is called with (repoName, done, total) as repos are prepared.
func (p *Pipeline) Run(spec Spec, progress func(string, int, int)) (*Result, error) {
	start := time.Now()

	// Per-user clone cache (persists on a warm instance across refreshes). When
	// no cache root is set, use a scratch dir wiped at job end (docs 07).
	cacheDir := ""
	var cleanup func()
	if p.Cache != "" {
		cacheDir = filepath.Join(p.Cache, safeName(spec.Login))
		_ = os.MkdirAll(cacheDir, 0o755)
	} else {
		tmp, err := os.MkdirTemp("", "agentstats-job-*")
		if err != nil {
			return nil, fmt.Errorf("scratch: %w", err)
		}
		cacheDir = tmp
		cleanup = func() { _ = os.RemoveAll(tmp) }
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Clone/fetch repos in parallel. JobCloneWorkers bounds one job; the package-level
	// cloneSem (GlobalCloneLimit) still caps clones across ALL jobs, and the cloner's
	// Retry-After backoff keeps us inside GitHub's limits (doc 13 fix 3).
	type prepared struct {
		repo  analyzer.Repo
		state store.RepoState
		priv  bool
		ok    bool
	}
	total := len(spec.Repos)
	results := make([]prepared, total)
	var done int64
	var progMu sync.Mutex
	spec.Live.Step("cloning", "", 0, total, -1, -1)
	report := func(name string) {
		n := atomic.AddInt64(&done, 1)
		spec.Live.Step("cloning", displayName(name, privateSet(spec.Repos)), int(n), total, -1, -1)
		if progress != nil {
			progMu.Lock()
			progress(name, int(n), total)
			progMu.Unlock()
		}
	}
	idx := make(chan int)
	var wg sync.WaitGroup
	workers := JobCloneWorkers
	if workers > total {
		workers = total
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				rs := spec.Repos[i]
				gitDir, headSHA := rs.GitDir, ""
				if gitDir == "" && p.Cloner != nil {
					gd, sha, _, err := p.Cloner.Ensure(rs, cacheDir, spec.Token)
					if err != nil {
						// One repo failing must not fail the whole job (docs 02/13).
						log.Printf("job %s: repo %d/%d skipped: %v", spec.Login, i+1, total, err)
						report(rs.FullName)
						continue
					}
					gitDir, headSHA = gd, sha
				}
				if gitDir != "" {
					results[i] = prepared{
						repo:  analyzer.Repo{GitDir: gitDir, Name: shortName(rs.FullName), Source: "github"},
						state: store.RepoState{GithubID: spec.GithubID, FullName: rs.FullName, LastSeenSHA: headSHA, PushedAt: rs.PushedAt},
						priv:  rs.Private,
						ok:    true,
					}
				}
				report(rs.FullName)
			}
		}()
	}
	for i := range spec.Repos {
		idx <- i
	}
	close(idx)
	wg.Wait()

	var repos []analyzer.Repo
	private := map[string]bool{}
	var newStates []store.RepoState
	for _, r := range results {
		if !r.ok {
			continue
		}
		if r.priv {
			private[r.repo.Name] = true
		}
		repos = append(repos, r.repo)
		newStates = append(newStates, r.state)
	}

	a := &analyzer.Analyzer{
		Repos:        repos,
		Emails:       emailSet(spec.Emails),
		AgentStart:   orDefault(spec.AgentStart, analyzer.AutoEra),
		AgentRules:   analyzer.DefaultAgentRules(),
		PrivateRepos: private,
		OnStep: func(phase, repo string, done, total, commits, lines int) {
			spec.Live.Step(phase, repo, done, total, commits, lines)
		},
	}
	stats, err := a.Run(spec.Login, nil)
	if err != nil {
		return nil, fmt.Errorf("analyze: %w", err)
	}
	stats.NormalizeEras()

	version := 1
	if p.Version != nil {
		version = p.Version(spec.Login)
	}

	spec.Live.Step("rendering", "", 0, 0, -1, -1)
	// Render + upload every artifact. Upload is best-effort: an unconfigured or
	// failing R2 must not fail the job — the dashboard renders
	// cards inline from the stats JSON regardless.
	if err := p.renderAndUpload(spec.Login, version, stats); err != nil {
		log.Printf("job %s: r2 upload: %s", spec.Login, Redact(err.Error()))
	}

	// Persist aggregates + incremental cursors.
	statsJSON, _ := json.Marshal(stats)
	_ = p.Store.SaveStats(store.StatsRow{
		GithubID:   spec.GithubID,
		Version:    version,
		ComputedAt: nowUTC(),
		StatsJSON:  string(statsJSON),
		Headline:   render.Card1Headline(stats),
	})
	if len(newStates) > 0 {
		_ = p.Store.SaveRepoStates(newStates)
	}

	return &Result{
		Stats:   stats,
		Repos:   stats.Totals.Repos,
		Commits: stats.Totals.Commits,
		Seconds: int(time.Since(start).Seconds()),
	}, nil
}

// renderAndUpload renders both cards (light+dark), the badge and the og.png, and
// uploads them to R2 under u/<login>/v<n>/… . Returns nil when R2 is not wired.
func (p *Pipeline) renderAndUpload(login string, version int, s *analyzer.Stats) error {
	svgs := map[string]string{
		"badge.svg":             render.Badge(s, render.Light),
		"before-after.svg":      render.BeforeAfter(s, render.Light),
		"before-after.dark.svg": render.BeforeAfter(s, render.Dark),
		"composition.svg":       render.Composition(s, render.Light),
		"composition.dark.svg":  render.Composition(s, render.Dark),
		"og.svg":                render.OG(s, render.Light),
	}
	var objs []r2.Object
	for name, svg := range svgs {
		objs = append(objs, r2.Object{
			Key: r2.VersionKey(login, version, name), Body: []byte(svg), ContentType: "image/svg+xml",
		})
	}
	// PNGs for social embeds (best-effort; needs resvg in the image).
	for _, base := range []string{"before-after.svg", "composition.svg", "og.svg"} {
		if png, err := render.PNG([]byte(svgs[base])); err == nil {
			name := strings.TrimSuffix(base, ".svg") + ".png"
			if base == "og.svg" {
				name = "og.png"
			}
			objs = append(objs, r2.Object{Key: r2.VersionKey(login, version, name), Body: png, ContentType: "image/png"})
		}
	}
	if p.R2 == nil || !p.R2.Enabled() {
		return nil
	}
	if err := p.R2.PutAll(login, version, objs); err != nil {
		return err
	}
	// Keep v<n> and v<n-1>; drop anything older (docs 04).
	return p.R2.PruneVersions(login, version)
}

func shortName(fullName string) string {
	if i := strings.LastIndex(fullName, "/"); i >= 0 {
		return fullName[i+1:] + ".git"
	}
	return fullName + ".git"
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

func emailSet(list []string) map[string]bool {
	m := map[string]bool{}
	for _, e := range list {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			m[e] = true
		}
	}
	return m
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func nowUTC() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }

// privateSet returns the full names of private repos in specs.
func privateSet(specs []RepoSpec) map[string]bool {
	m := map[string]bool{}
	for _, r := range specs {
		if r.Private {
			m[r.FullName] = true
		}
	}
	return m
}

// displayName masks private repo names for anything a user or viewer may see.
func displayName(fullName string, private map[string]bool) string {
	if private[fullName] {
		return "a private repo"
	}
	return shortName(fullName)
}
