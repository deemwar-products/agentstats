package jobs

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GlobalCloneLimit caps concurrent git clone/fetch across ALL jobs so mass
// egress from the container never trips GitHub's secondary/abuse limits (doc 13
// fix 3). It is a package-level semaphore shared by every GitCloner.
const GlobalCloneLimit = 20

var cloneSem = make(chan struct{}, GlobalCloneLimit)

// GitCloner bare-clones (or incrementally fetches) repos with the user token via
// an in-memory http.extraHeader — never in a URL, remote or on-disk git config
// (docs 05). It reuses a per-user cache dir so a refresh fetches deltas instead
// of re-cloning (doc 13 fix 3).
type GitCloner struct {
	// MaxRetries on a 429/403 secondary-limit response.
	MaxRetries int
}

var _ Cloner = (*GitCloner)(nil)

// Ensure clones or fetches spec into cacheDir and returns the HEAD sha.
func (g *GitCloner) Ensure(spec RepoSpec, cacheDir, token string) (string, string, bool, error) {
	gitDir := filepath.Join(cacheDir, safeName(spec.FullName)+".git")

	cloneSem <- struct{}{}
	defer func() { <-cloneSem }()

	if _, err := os.Stat(gitDir); err == nil {
		// Cached clone present. A refresh with unchanged pushed_at skips the
		// network entirely; otherwise fetch the new commits.
		head, _ := gitHead(gitDir)
		if spec.PushedAt != "" && head != "" {
			// Caller decided reuse-vs-fetch by pushed_at; we fetch to be safe when
			// pushed_at moved. Cheap when nothing changed.
		}
		if err := g.withBackoff(func() error { return fetch(gitDir, token) }); err != nil {
			// Fetch failed — fall back to the cached state rather than failing.
			return gitDir, head, false, nil
		}
		newHead, _ := gitHead(gitDir)
		return gitDir, newHead, newHead == head, nil
	}

	if err := g.withBackoff(func() error { return bareClone(spec.CloneURL, gitDir, token) }); err != nil {
		return "", "", false, err
	}
	head, _ := gitHead(gitDir)
	return gitDir, head, false, nil
}

// withBackoff retries fn on a GitHub secondary-rate-limit signal, honouring
// Retry-After when present, with jittered exponential backoff otherwise.
func (g *GitCloner) withBackoff(fn func() error) error {
	max := g.MaxRetries
	if max <= 0 {
		max = 3
	}
	var lastErr error
	for attempt := 0; attempt <= max; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !isRateLimited(lastErr) {
			return lastErr
		}
		wait := retryAfter(lastErr)
		if wait <= 0 {
			base := time.Duration(1<<attempt) * time.Second
			jitter := time.Duration(rand.Int63n(int64(time.Second)))
			wait = base + jitter
		}
		time.Sleep(wait)
	}
	return lastErr
}

func isRateLimited(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "429") || strings.Contains(s, "secondary rate") ||
		strings.Contains(s, "abuse") || strings.Contains(s, "403")
}

var retryAfterRe = regexp.MustCompile(`(?i)retry-after[:= ]+(\d+)`)

func retryAfter(err error) time.Duration {
	if m := retryAfterRe.FindStringSubmatch(err.Error()); len(m) > 1 {
		if n, e := strconv.Atoi(m[1]); e == nil {
			return time.Duration(n) * time.Second
		}
	}
	return 0
}

// bareClone clones a repo bare (no working tree), token via an in-memory
// http.extraHeader on the command line only (docs 05).
//
// No --filter=blob:none: the analyzer's `git log --numstat` needs the blobs, and
// in a partial clone git would lazily fetch them later WITHOUT the auth header
// (private repos would fail or prompt). A full bare clone keeps every later git
// call offline.
func bareClone(cloneURL, dst, token string) error {
	// Never accept credentials embedded in a URL: they would land in the
	// on-disk remote config.
	if u, err := url.Parse(cloneURL); err == nil && u.User != nil {
		return fmt.Errorf("refusing clone URL with embedded credentials")
	}
	args := tokenArgs(token)
	args = append(args, "clone", "--bare", "--quiet", cloneURL, dst)
	return git(args...)
}

// fetch pulls new commits into an existing bare clone (incremental refresh).
// A bare clone has no remote.origin.fetch refspec, so pass it explicitly to
// actually move refs/heads/*.
func fetch(gitDir, token string) error {
	args := append(tokenArgs(token), "--git-dir", gitDir, "fetch", "--quiet", "--prune", "origin",
		"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
	return git(args...)
}

// tokenArgs passes the token to ONE git invocation as `-c http.extraHeader=...`.
// It is never written to a URL, a remote, or a config file on disk. GitHub's git
// endpoint rejects Bearer; it needs Basic with user "x-access-token" (the GitHub
// Actions form), which works for App user and installation tokens.
func tokenArgs(token string) []string {
	if token == "" {
		return nil
	}
	cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return []string{"-c", "http.extraHeader=Authorization: Basic " + cred}
}

func gitHead(gitDir string) (string, error) {
	cmd := exec.Command("git", "--git-dir", gitDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, Redact(string(out)))
	}
	return nil
}
