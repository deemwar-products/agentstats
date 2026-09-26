package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/render"
)

// analyzeRequest is what the UserJob Durable Object POSTs to /analyze. The token
// lives only in this process's memory for the job and is never written to disk.
type analyzeRequest struct {
	Token      string     `json:"token"`
	Emails     []string   `json:"emails"`
	Repos      []repoSpec `json:"repos"`
	AgentStart string     `json:"agent_start"`
	Login      string     `json:"login"`
}

type repoSpec struct {
	FullName string `json:"full_name"`
	CloneURL string `json:"clone_url"`
	Fork     bool   `json:"fork"`
	Private  bool   `json:"private"`
}

// tokenRe redacts GitHub tokens from any log line (defence in depth).
var tokenRe = regexp.MustCompile(`gh[opsu]_[A-Za-z0-9]+|ghu_[A-Za-z0-9]+`)

func redact(s string) string { return tokenRe.ReplaceAllString(s, "gh_REDACTED") }

func runServe(args []string) {
	addr := ":8080"
	for i := 0; i < len(args); i++ {
		if args[i] == "--addr" {
			addr, i = next(args, i)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/analyze", handleAnalyze)
	log.Printf("analyzer serve on %s", addr)
	// NOTE: the container is reachable ONLY from the Durable Object via the
	// Containers binding — it is never exposed as a public route (docs 07).
	log.Fatal(http.ListenAndServe(addr, mux))
}

func handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req analyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.AgentStart == "" {
		req.AgentStart = "2025-01"
	}
	if req.Login == "" {
		req.Login = "user"
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	emit := func(v any) {
		_ = enc.Encode(v)
		if flusher != nil {
			flusher.Flush()
		}
	}

	// Scratch dir wiped at the end — code never persists (docs 07).
	scratch, err := os.MkdirTemp("", "agentstats-job-*")
	if err != nil {
		emit(map[string]string{"type": "error", "error": "scratch dir"})
		return
	}
	defer os.RemoveAll(scratch)

	// Bare-clone each repo with the user token, then count.
	var repos []analyzer.Repo
	private := map[string]bool{}
	for i, rs := range req.Repos {
		dst := filepath.Join(scratch, fmt.Sprintf("r%d.git", i))
		if err := bareClone(rs.CloneURL, dst, req.Token); err != nil {
			log.Printf("clone %s: %s", rs.FullName, redact(err.Error()))
			emit(map[string]any{"type": "progress", "repo": rs.FullName, "done": i + 1, "total": len(req.Repos), "skipped": true})
			continue
		}
		name := shortName(rs.FullName)
		if rs.Private {
			private[name] = true // its name is redacted to "a private repo" in output
		}
		repos = append(repos, analyzer.Repo{GitDir: dst, Name: name, Source: "github"})
		emit(map[string]any{"type": "progress", "repo": rs.FullName, "done": i + 1, "total": len(req.Repos)})
	}

	a := &analyzer.Analyzer{
		Repos:        repos,
		Emails:       emailSet(strings.Join(req.Emails, ",")),
		AgentStart:   req.AgentStart,
		AgentRules:   analyzer.DefaultAgentRules(),
		PrivateRepos: private,
	}
	stats, err := a.Run(req.Login, nil)
	if err != nil {
		emit(map[string]string{"type": "error", "error": "analyze failed"})
		return
	}

	// Render + upload every card/badge to R2, then emit the aggregate result.
	if err := renderAndUpload(req.Login, stats); err != nil {
		log.Printf("render/upload: %v", err)
	}
	emit(map[string]any{"type": "result", "stats": stats})
}

// bareClone clones a repo as a bare mirror with NO working-tree checkout, using
// the token via an in-memory http.extraHeader — never in the URL, a remote, or
// a git config file on disk (docs 05).
func bareClone(cloneURL, dst, token string) error {
	args := []string{}
	if token != "" {
		args = append(args, "-c", "http.extraHeader=Authorization: Bearer "+token)
	}
	args = append(args, "clone", "--bare", "--filter=blob:none", cloneURL, dst)
	// NOTE: blob:none makes the initial clone cheap; numstat then backfills the
	// blobs it needs. If numstat proves too slow on partial clones, drop the
	// filter for a full bare clone (docs 02 discusses this trade-off).
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, redact(string(out)))
	}
	return nil
}

func shortName(fullName string) string {
	if i := strings.LastIndex(fullName, "/"); i >= 0 {
		return fullName[i+1:] + ".git"
	}
	return fullName + ".git"
}

// renderAndUpload renders the cards + badge and uploads them to R2 over the S3
// API. R2 credentials come from the container env (never hardcoded).
//
// TODO(owner/launch): wire the S3 client. Set R2_ACCESS_KEY_ID,
// R2_SECRET_ACCESS_KEY, R2_BUCKET (agentstats-cards) and R2_ENDPOINT
// (https://<ACCOUNT_ID>.r2.cloudflarestorage.com) as container secrets via
// `wrangler secret put` / `sec`. Upload order per docs 02/03: write
// u/<login>/v<n>/… first, then flip u/<login>/current.json = {"v":n}.
func renderAndUpload(login string, s *analyzer.Stats) error {
	artifacts := map[string][]byte{
		"badge.svg":            []byte(render.Badge(s, render.Light)),
		"before-after.svg":     []byte(render.BeforeAfter(s, render.Light)),
		"composition.svg":      []byte(render.Composition(s, render.Light)),
		"before-after.dark.svg": []byte(render.BeforeAfter(s, render.Dark)),
		"composition.dark.svg":  []byte(render.Composition(s, render.Dark)),
	}
	// PNGs for social embeds (docs 06). Best-effort: the container always has resvg.
	for _, base := range []string{"before-after.svg", "composition.svg"} {
		if png, err := render.PNG(artifacts[base]); err == nil {
			artifacts[strings.TrimSuffix(base, ".svg")+".png"] = png
		}
	}

	endpoint := os.Getenv("R2_ENDPOINT")
	bucket := os.Getenv("R2_BUCKET")
	if endpoint == "" || bucket == "" || os.Getenv("R2_ACCESS_KEY_ID") == "" {
		// Not configured — this is expected in local/dev runs. The Worker serves
		// a placeholder until a real job uploads (docs 03).
		log.Printf("R2 not configured (R2_ENDPOINT/R2_BUCKET/R2_ACCESS_KEY_ID); skipping upload of %d artifacts for %s", len(artifacts), login)
		return nil
	}
	// TODO(owner/launch): perform the S3 PutObject calls here with
	// aws-sdk-go-v2 against `endpoint`/`bucket`, keying u/<login>/v<n>/<name>,
	// then PutObject u/<login>/current.json = {"v":n}. Left unimplemented so the
	// build carries no cloud credentials and no network dependency.
	log.Printf("R2 configured; %d artifacts ready to upload for %s (S3 client wiring is a launch TODO)", len(artifacts), login)
	return nil
}
