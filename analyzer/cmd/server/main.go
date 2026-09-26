// Command server is the agentstats single-container backend (doc 12): the
// server-rendered web UI, GitHub App sign-in, dashboard, badge builder, and the
// analysis-job queue — all in one Go service. It is the container ENTRYPOINT.
//
// D1 and R2 access both go through the front Worker's internal routes
// (/_internal/d1, /_internal/r2 — doc 13 fix 1), so the container holds no
// Cloudflare API or S3 token. All secrets are read from env at runtime — never hardcoded. Placeholder
// values elsewhere in the repo are OBVIOUSLY fake.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/deemwar-products/agentstats/analyzer/jobs"
	"github.com/deemwar-products/agentstats/analyzer/r2"
	"github.com/deemwar-products/agentstats/analyzer/store"
	"github.com/deemwar-products/agentstats/analyzer/web"
)

func main() {
	// Every log line passes through the GitHub-token redactor (docs 05).
	log.SetOutput(jobs.RedactWriter{W: os.Stderr})

	if os.Getenv("GITHUB_CLIENT_ID") != "" && os.Getenv("SESSION_KEY") == "" {
		log.Fatal("GITHUB_CLIENT_ID is set but SESSION_KEY is not: refusing to sign sessions with the dev key")
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	// Store: D1 via the front Worker's internal route when configured, else an
	// in-memory store for local dev. A hot-read cache (doc 13 fix 1) fronts it.
	var base store.Store
	if d1, ok := store.NewD1FromEnv(); ok {
		base = d1
		log.Printf("store: D1 via internal Worker route")
	} else {
		base = store.NewMemory()
		log.Printf("store: in-memory (dev; set INTERNAL_D1_URL + INTERNAL_D1_SECRET for D1)")
	}
	st := store.NewCache(base, 60*time.Second)

	// Analysis pipeline + queue. GitCloner does incremental fetch/clone with the
	// global clone-concurrency cap and Retry-After backoff (doc 13 fix 3).
	instance := os.Getenv("CF_INSTANCE_ID")
	if instance == "" {
		instance, _ = os.Hostname()
	}
	cloneCache := os.Getenv("AGENTSTATS_CLONE_CACHE") // persistent per-user clone cache dir (warm instances)
	r2c := r2.FromEnv()
	if r2c.Enabled() {
		log.Printf("r2: via internal Worker route")
	} else {
		log.Printf("r2: disabled (dev; cards render inline). Set INTERNAL_D1_URL + INTERNAL_D1_SECRET")
	}
	pipeline := &jobs.Pipeline{
		Store:   st,
		R2:      r2c,
		Cloner:  &jobs.GitCloner{MaxRetries: 3},
		Cache:   cloneCache,
		Version: nextVersion(st),
	}
	runner := jobs.NewRunner(st, pipeline, instance)
	runner.Start()
	defer runner.Stop()

	srv, err := web.New(st, runner)
	if err != nil {
		log.Fatalf("web.New: %v", err)
	}

	log.Printf("agentstats server on %s (instance %s)", addr, instance)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

// nextVersion returns the next R2 version number for a user (previous + 1).
func nextVersion(st store.Store) func(login string) int {
	return func(login string) int {
		u, err := st.GetUserByLogin(login)
		if err != nil {
			return 1
		}
		row, err := st.GetStats(u.GithubID)
		if err != nil {
			return 1
		}
		return row.Version + 1
	}
}
