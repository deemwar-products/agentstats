package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The five verified author emails used by the prototype and the fixture.
var refEmails = map[string]bool{
	"muthuishere@gmail.com":       true,
	"muthuisheremobile@gmail.com": true,
	"admin@deemwar.com":           true,
	"io@deemwar.com":              true,
	"oss@deemwar.com":             true,
}

const refGlob = "github-code/*/*/*.git" // under ~/data-exporter-local/raw

// expectedFixture mirrors testdata/expected-github-only.json.
type expectedFixture struct {
	ReposGithub   int                       `json:"repos_github"`
	FirstYear     string                    `json:"first_year"`
	Totals        Totals                    `json:"totals"`
	CommitsByYear map[string]int            `json:"commits_by_year"`
	LinesByYear   map[string]int            `json:"lines_by_year"`
	LangByYear    map[string]map[string]int `json:"lang_by_year"`
	TopMonths     [][]any                   `json:"top_months"`
	PeakMonth     PeakMonth                 `json:"peak_month"`
	Dropped       []DroppedCommit           `json:"dropped"`
	Eras          struct {
		Before Era `json:"before"`
		After  Era `json:"after"`
	} `json:"eras"`
}

// TestRegressionGitHubOnly runs the analyzer over the same local bare clones the
// prototype uses and asserts the GitHub-only aggregates reproduce exactly. It
// SKIPS with a clear message when the clones are not present on this machine;
// the synthetic-repo test below still covers every counting rule in that case.
func TestRegressionGitHubOnly(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	glob := filepath.Join(home, "data-exporter-local", "raw", refGlob)
	matches, _ := filepath.Glob(glob)
	if len(matches) == 0 {
		t.Skipf("reference clones not present (%s) — run on the machine that holds them; synthetic tests still cover the rules", glob)
	}
	sort.Strings(matches)

	fx := loadFixture(t)

	repos := make([]Repo, 0, len(matches))
	for _, m := range matches {
		repos = append(repos, Repo{GitDir: m, Name: filepath.Base(m), Source: "github"})
	}
	a := &Analyzer{Repos: repos, Emails: refEmails, AgentStart: "2025-01", AgentRules: DefaultAgentRules()}
	got, err := a.Run("muthuishere", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.ReposGithub != fx.ReposGithub {
		t.Errorf("repos_github = %d, want %d", got.ReposGithub, fx.ReposGithub)
	}
	assertIntMap(t, "commits_by_year", got.CommitsByYear, fx.CommitsByYear)
	assertIntMap(t, "lines_by_year", got.LinesByYear, fx.LinesByYear)
	assertLangByYear(t, got.LangByYear, fx.LangByYear)

	// top-6 months.
	gotTop := topN(got.Months, 6)
	for i, want := range fx.TopMonths {
		wm := want[0].(string)
		wl := int(want[1].(float64))
		if i >= len(gotTop) || gotTop[i][0] != wm || atoi(gotTop[i][1]) != wl {
			t.Errorf("top_months[%d] = %v, want [%s %d]", i, gotTop[i], wm, wl)
		}
	}

	// dropped commits: assert the SET of (month,sha,files,lines) — repo name is
	// intentionally ignored (the fixture labels them generically).
	assertDropped(t, got.Dropped, fx.Dropped)

	// era rollups.
	assertEra(t, "before", got.Eras.Before, fx.Eras.Before, false)
	assertEra(t, "after", got.Eras.After, fx.Eras.After, true)

	if got.PeakMonth.Month != fx.PeakMonth.Month || got.PeakMonth.Lines != fx.PeakMonth.Lines {
		t.Errorf("peak_month = %+v, want %+v", got.PeakMonth, fx.PeakMonth)
	}
	if !t.Failed() {
		t.Logf("reproduced GitHub-only reference exactly: %d repos, %d dropped commits, totals %d lines / %d commits",
			got.ReposGithub, len(got.Dropped), got.Totals.Lines, got.Totals.Commits)
	}
}

func loadFixture(t *testing.T) expectedFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "expected-github-only.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx expectedFixture
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return fx
}

func assertIntMap(t *testing.T, name string, got, want map[string]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %d keys, want %d", name, len(got), len(want))
	}
	for k, wv := range want {
		if got[k] != wv {
			t.Errorf("%s[%s] = %d, want %d", name, k, got[k], wv)
		}
	}
}

func assertLangByYear(t *testing.T, got, want map[string]map[string]int) {
	t.Helper()
	for y, wl := range want {
		assertIntMap(t, "lang_by_year["+y+"]", got[y], wl)
	}
	if len(got) != len(want) {
		t.Errorf("lang_by_year: got %d years, want %d", len(got), len(want))
	}
}

func assertDropped(t *testing.T, got, want []DroppedCommit) {
	t.Helper()
	key := func(d DroppedCommit) string {
		return fmt.Sprintf("%s|%s|%d|%d", d.Month, d.SHA, d.Files, d.Lines)
	}
	gs := map[string]bool{}
	for _, d := range got {
		gs[key(d)] = true
	}
	if len(got) != len(want) {
		t.Errorf("dropped: got %d, want %d", len(got), len(want))
	}
	for _, d := range want {
		if !gs[key(d)] {
			t.Errorf("dropped: missing (month=%s sha=%s files=%d lines=%d)", d.Month, d.SHA, d.Files, d.Lines)
		}
	}
}

func assertEra(t *testing.T, name string, got, want Era, after bool) {
	t.Helper()
	if got.Lines != want.Lines {
		t.Errorf("%s.lines = %d, want %d", name, got.Lines, want.Lines)
	}
	if got.Commits != want.Commits {
		t.Errorf("%s.commits = %d, want %d", name, got.Commits, want.Commits)
	}
	if got.ReposTouched != want.ReposTouched {
		t.Errorf("%s.repos_touched = %d, want %d", name, got.ReposTouched, want.ReposTouched)
	}
	if got.LinesPerCommit != want.LinesPerCommit {
		t.Errorf("%s.lines_per_commit = %d, want %d", name, got.LinesPerCommit, want.LinesPerCommit)
	}
	if got.DailyLangs != want.DailyLangs {
		t.Errorf("%s.daily_langs = %d, want %d", name, got.DailyLangs, want.DailyLangs)
	}
	if after {
		if got.PaceThisYear != want.PaceThisYear {
			t.Errorf("%s.pace_this_year = %d, want %d", name, got.PaceThisYear, want.PaceThisYear)
		}
	} else if got.PacePerYear != want.PacePerYear {
		t.Errorf("%s.pace_per_year = %d, want %d", name, got.PacePerYear, want.PacePerYear)
	}
}

func topN(m map[string]int, n int) [][2]any {
	type kv struct {
		k string
		v int
	}
	list := make([]kv, 0, len(m))
	for k, v := range m {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	out := [][2]any{}
	for i := 0; i < n && i < len(list); i++ {
		out = append(out, [2]any{list[i].k, list[i].v})
	}
	return out
}

func atoi(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

// ---- synthetic-repo test: covers every counting rule without needing the real clones ----

func TestSyntheticRules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
	root := t.TempDir()
	repoA := filepath.Join(root, "A")
	gitInit(t, repoA)

	// C1 (me, 2020-03, before era): 10 Go lines counted; README (notcode),
	// lockfile (skip), vendored file (skip) all excluded.
	commit(t, repoA, "me@test.com", "Me", "2020-03-01T00:00:00", "feat: start", map[string]string{
		"main.go":           lines(10),
		"README.md":         lines(50),
		"package-lock.json": lines(999),
		"vendor/dep.go":     lines(100),
	})
	// C2 (other author): fully excluded.
	commit(t, repoA, "other@test.com", "Other", "2020-04-01T00:00:00", "not me", map[string]string{
		"stranger.go": lines(500),
	})
	// C3 (me, 2025-03, after era): a single file adding >5000 lines is skipped,
	// but the commit is still counted in commits_by_year.
	commit(t, repoA, "me@test.com", "Me", "2025-03-01T00:00:00", "big paste", map[string]string{
		"blob.go": lines(6000),
	})
	// C4 (me, 2025-04): agent-signed (Claude Code trailer), 20 counted lines.
	commit(t, repoA, "me@test.com", "Me", "2025-04-01T00:00:00",
		"feat: agent work\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>",
		map[string]string{"svc.go": lines(20)})
	// C5 (me, 2025-05): bulk-import by file count (>300 files) → dropped.
	bulk := map[string]string{}
	for i := 0; i < 301; i++ {
		bulk[fmt.Sprintf("gen/f%d.go", i)] = lines(1)
	}
	commit(t, repoA, "me@test.com", "Me", "2025-05-01T00:00:00", "import a tree", bulk)

	// Repo B is a bare mirror of A: identical SHAs must be deduped, not doubled.
	repoB := filepath.Join(root, "B.git")
	run(t, root, "git", "clone", "--bare", "--quiet", repoA, repoB)

	a := &Analyzer{
		Repos: []Repo{
			{GitDir: filepath.Join(repoA, ".git"), Name: "A.git", Source: "github"},
			{GitDir: repoB, Name: "B.git", Source: "github"},
		},
		Emails:     map[string]bool{"me@test.com": true},
		AgentStart: "2025-01",
		AgentRules: DefaultAgentRules(),
	}
	s, err := a.Run("me", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Author filter + notcode/lockfile/vendor skips: only main.go's 10 Go lines
	// count in 2020.
	if s.LinesByYear["2020"] != 10 {
		t.Errorf("2020 lines = %d, want 10 (notcode/lockfile/vendor + other-author excluded)", s.LinesByYear["2020"])
	}
	if s.LangByYear["2020"]["Go"] != 10 || len(s.LangByYear["2020"]) != 1 {
		t.Errorf("2020 langs = %v, want {Go:10}", s.LangByYear["2020"])
	}
	// Dedup: 4 of my commits are in 2020(1)+2025(3); the other-author commit is
	// excluded. Despite two repos carrying every SHA, commits are counted once.
	if s.CommitsByYear["2020"] != 1 {
		t.Errorf("2020 commits = %d, want 1 (dedup across A + mirror B)", s.CommitsByYear["2020"])
	}
	if s.CommitsByYear["2025"] != 3 {
		t.Errorf("2025 commits = %d, want 3 (big-paste + agent + dropped bulk all counted)", s.CommitsByYear["2025"])
	}
	// >5000-line file skipped: blob.go contributes 0; only svc.go's 20 lines
	// count in 2025 (the 301x1 bulk commit is dropped).
	if s.LinesByYear["2025"] != 20 {
		t.Errorf("2025 lines = %d, want 20 (blob>5000 skipped, bulk commit dropped)", s.LinesByYear["2025"])
	}
	// Bulk-import drop recorded once (deduped by SHA), by file count.
	if len(s.Dropped) != 1 {
		t.Fatalf("dropped = %d commits, want 1", len(s.Dropped))
	}
	if s.Dropped[0].Files != 301 {
		t.Errorf("dropped files = %d, want 301", s.Dropped[0].Files)
	}
	// Era split.
	if s.Eras.Before.Lines != 10 || s.Eras.After.Lines != 20 {
		t.Errorf("era lines before/after = %d/%d, want 10/20", s.Eras.Before.Lines, s.Eras.After.Lines)
	}
	// Attribution: exactly one Claude-Code-signed commit, model extracted.
	if s.Agents == nil {
		t.Fatal("agents block missing")
	}
	if s.Agents.SignedCommits != 1 {
		t.Errorf("signed_commits = %d, want 1", s.Agents.SignedCommits)
	}
	cc := s.Agents.ByTool["Claude Code"]
	if cc == nil || cc.Commits != 1 || cc.Lines != 20 {
		t.Errorf("Claude Code tool stat = %+v, want commits=1 lines=20", cc)
	}
	if cc != nil && cc.Models["Claude Opus 4.5"] != 1 {
		t.Errorf("model extraction = %v, want {Claude Opus 4.5:1}", cc.Models)
	}
	if s.Agents.FirstSignedMonth != "2025-04" {
		t.Errorf("first_signed_month = %q, want 2025-04", s.Agents.FirstSignedMonth)
	}
}

// TestBulkImportByLines covers the >25,000-line drop path (files <= 300).
func TestBulkImportByLines(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "R")
	gitInit(t, repo)
	// 10 files each adding 3000 lines = 30,000 > 25,000, files=10 <= 300 → drop.
	files := map[string]string{}
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("m%d.go", i)] = lines(3000)
	}
	commit(t, repo, "me@test.com", "Me", "2025-06-01T00:00:00", "generated", files)
	a := &Analyzer{
		Repos:      []Repo{{GitDir: filepath.Join(repo, ".git"), Name: "R.git", Source: "github"}},
		Emails:     map[string]bool{"me@test.com": true},
		AgentStart: "2025-01",
	}
	s, err := a.Run("me", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(s.Dropped) != 1 || s.Dropped[0].Lines != 30000 {
		t.Errorf("dropped = %+v, want 1 commit with 30000 lines", s.Dropped)
	}
	if s.LinesByYear["2025"] != 0 {
		t.Errorf("2025 lines = %d, want 0 (whole commit dropped)", s.LinesByYear["2025"])
	}
}

// ---- git test helpers ----

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "init", "--quiet", "-b", "main")
	run(t, dir, "git", "config", "user.email", "setup@test.com")
	run(t, dir, "git", "config", "user.name", "Setup")
	// Isolate from any global hooks (this machine ships a commit-msg hook that
	// strips Co-Authored-By trailers) so the synthetic agent-signed commit keeps
	// its trailer for the attribution assertions.
	run(t, dir, "git", "config", "core.hooksPath", "/dev/null")
}

func commit(t *testing.T, dir, email, name, date, msg string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(t, dir, "git", "add", "-A")
	cmd := exec.Command("git", "-c", "user.email="+email, "-c", "user.name="+name,
		"-c", "core.hooksPath=/dev/null",
		"commit", "--quiet", "--no-verify", "--date="+date, "-m", msg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_EMAIL="+email, "GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_EMAIL="+email, "GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
}

// lines returns n lines of code-ish text (each distinct so numstat counts n).
func lines(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "x%d := %d\n", i, i)
	}
	return b.String()
}
