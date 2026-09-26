// Package store is the persistence layer for the agentstats container: the D1
// tables (users, stats, jobs, badges, repo_state) behind a small interface.
//
// Per docs 12 and 13 the Go container does NOT talk to the Cloudflare D1 REST
// API (account-wide rate limit). It reaches D1 through the front Worker's
// internal `/_internal/d1` route, which runs the query with the D1 *binding*
// (no REST limit) — see d1.go. A hot-read cache sits in front (see cache.go).
// The in-memory implementation (memory.go) backs local dev and the tests.
//
// It holds AGGREGATES ONLY — never code, tokens or verified emails. Those live
// in the job's process memory for the duration of one job (docs 04, 07).
// repo_state (last_seen_sha, pushed_at) is operational metadata for incremental
// refresh (doc 13 fix 3), never rendered publicly.
package store

import "errors"

// ErrNotFound is returned when a lookup has no row.
var ErrNotFound = errors.New("store: not found")

// Job status values (the jobs table IS the queue — doc 13 §Job queue).
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// Job kinds and lanes for fairness (doc 13).
const (
	KindFirst   = "first"   // a user's first-ever analysis — served before refreshes
	KindRefresh = "refresh" // a re-analysis
	LaneNormal  = "normal"
	LaneBig     = "big" // > BigAccountRepos repos — a separate lane so it never blocks others
)

// BigAccountRepos is the repo count above which a job goes to the big lane.
const BigAccountRepos = 300

// User is a row of the D1 `users` table.
type User struct {
	GithubID      int64
	Login         string
	Name          string
	AvatarURL     string
	AgentStart    string // YYYY-MM
	IsPublic      bool
	ExcludedRepos []string // full_name list
	NotifyEmail   bool     // opt-in notify-when-done (doc 13)
	// Leaderboard: ranked on /leaderboard (opt-out, DB default 1). UpsertUser
	// never changes it: new rows get the default (on), existing rows keep
	// theirs. Only UpdateSettings writes it.
	Leaderboard bool
	CreatedAt   string
	LastLoginAt string
}

// StatsRow is a row of the D1 `stats` table. StatsJSON is the analyzer's
// aggregate output (analyzer.Stats), stored verbatim.
type StatsRow struct {
	GithubID   int64
	Version    int
	ComputedAt string
	StatsJSON  string
	Headline   string
}

// LeaderboardRow is one ranked-eligible user plus their latest stats JSON.
type LeaderboardRow struct {
	User  User
	Stats StatsRow
}

// Job is a row of the D1 `jobs` table (the queue). Position is computed for the
// UI (place in line among queued jobs of the same lane), not stored.
type Job struct {
	ID         int64
	GithubID   int64
	Kind       string // first | refresh
	Lane       string // normal | big
	Status     string // queued | running | done | failed
	Position   int    // 1-based place in line while queued; 0 otherwise
	EnqueuedAt string
	StartedAt  string
	FinishedAt string
	Instance   string // container instance id holding the running row (single-flight)
	Heartbeat  string // last heartbeat; a stale running row can be reclaimed
	Repos      int
	Commits    int
	Seconds    int
	Error      string
}

// Badge is a row of the D1 `badges` table (the badge builder, doc 12).
type Badge struct {
	BadgeID   string
	GithubID  int64
	Metric    string
	Style     string
	Theme     string
	Label     string
	Accent    string
	Period    string
	CreatedAt string
}

// RepoState is one repo's incremental-refresh cursor (doc 13 fix 3). A refresh
// skips repos whose PushedAt is unchanged since the last successful analysis.
type RepoState struct {
	GithubID    int64
	FullName    string
	LastSeenSHA string
	PushedAt    string
}

// Store is the persistence contract the web + jobs layers depend on.
type Store interface {
	// Users.
	UpsertUser(u User) error
	GetUserByLogin(login string) (User, error)
	GetUserByID(githubID int64) (User, error)
	UpdateSettings(githubID int64, agentStart string, isPublic bool, excludedRepos []string, notifyEmail, leaderboard bool) error
	DeleteUser(githubID int64) error // cascades stats/jobs/badges/repo_state

	// Stats.
	SaveStats(s StatsRow) error
	GetStats(githubID int64) (StatsRow, error)
	// LeaderboardRows returns every user who is public AND on the leaderboard
	// and has stats, joined with their latest stats row.
	LeaderboardRows() ([]LeaderboardRow, error)

	// Jobs — the queue.
	// EnqueueJob adds a queued row, or returns the user's existing queued/running
	// job (existing=true) so a user never has two live jobs (single-flight).
	EnqueueJob(githubID int64, kind, lane, now string) (job Job, existing bool, err error)
	QueuedJobs() ([]Job, error)  // status=queued
	RunningJobs() ([]Job, error) // status=running
	// ClaimJob transitions queued→running iff still queued; ok=false if lost the race.
	ClaimJob(jobID int64, instance, now string) (ok bool, err error)
	Heartbeat(jobID int64, now string) error
	FinishJob(jobID int64, status, finishedAt string, repos, commits, seconds int, jobErr string) error
	// ReclaimStale marks running jobs with a heartbeat older than `before` as failed
	// so a crashed instance's job does not wedge the user's single-flight slot.
	ReclaimStale(before, now string) (int, error)
	GetJob(jobID int64) (Job, error)
	LatestJob(githubID int64) (Job, error)

	// Badges.
	SaveBadge(b Badge) error
	ListBadges(githubID int64) ([]Badge, error)
	GetBadge(badgeID string) (Badge, error)
	DeleteBadge(badgeID string, githubID int64) error

	// Incremental-refresh cursors (doc 13 fix 3).
	GetRepoStates(githubID int64) (map[string]RepoState, error)
	SaveRepoStates(states []RepoState) error
}
