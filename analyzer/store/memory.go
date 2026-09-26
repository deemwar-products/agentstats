package store

import (
	"fmt"
	"sort"
	"sync"
)

// Memory is an in-process Store used for local dev and tests. It behaves like
// the D1 schema (single-flight enqueue, atomic claim, cascade delete) without a
// network.
type Memory struct {
	mu      sync.Mutex
	users   map[int64]User
	stats   map[int64]StatsRow
	jobs    map[int64]Job
	badges  map[string]Badge
	repos   map[int64]map[string]RepoState // githubID -> full_name -> state
	nextJob int64
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		users:   map[int64]User{},
		stats:   map[int64]StatsRow{},
		jobs:    map[int64]Job{},
		badges:  map[string]Badge{},
		repos:   map[int64]map[string]RepoState{},
		nextJob: 1,
	}
}

var _ Store = (*Memory)(nil)

func (m *Memory) UpsertUser(u User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u.ExcludedRepos == nil {
		u.ExcludedRepos = []string{}
	}
	// Preserve created_at on update.
	prev, existed := m.users[u.GithubID]
	if existed && u.CreatedAt == "" {
		u.CreatedAt = prev.CreatedAt
	}
	// leaderboard mirrors D1: DB default 1 on insert, untouched on update.
	u.Leaderboard = !existed || prev.Leaderboard
	m.users[u.GithubID] = u
	return nil
}

func (m *Memory) GetUserByLogin(login string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if eqFold(u.Login, login) {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (m *Memory) GetUserByID(githubID int64) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[githubID]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *Memory) UpdateSettings(githubID int64, agentStart string, isPublic bool, excludedRepos []string, notifyEmail, leaderboard bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[githubID]
	if !ok {
		return ErrNotFound
	}
	u.AgentStart = agentStart
	u.IsPublic = isPublic
	if excludedRepos == nil {
		excludedRepos = []string{}
	}
	u.ExcludedRepos = excludedRepos
	u.NotifyEmail = notifyEmail
	u.Leaderboard = leaderboard
	m.users[githubID] = u
	return nil
}

func (m *Memory) DeleteUser(githubID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users, githubID)
	delete(m.stats, githubID)
	delete(m.repos, githubID)
	for id, j := range m.jobs {
		if j.GithubID == githubID {
			delete(m.jobs, id)
		}
	}
	for id, b := range m.badges {
		if b.GithubID == githubID {
			delete(m.badges, id)
		}
	}
	return nil
}

func (m *Memory) SaveStats(s StatsRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats[s.GithubID] = s
	return nil
}

func (m *Memory) GetStats(githubID int64) (StatsRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.stats[githubID]
	if !ok {
		return StatsRow{}, ErrNotFound
	}
	return s, nil
}

func (m *Memory) LeaderboardRows() ([]LeaderboardRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []LeaderboardRow
	for id, u := range m.users {
		st, ok := m.stats[id]
		if !ok || !u.IsPublic || !u.Leaderboard {
			continue
		}
		out = append(out, LeaderboardRow{User: u, Stats: st})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].User.GithubID < out[j].User.GithubID })
	return out, nil
}

// EnqueueJob is the single-flight gate: a queued or running row for the user
// returns the existing job instead of creating a second (existing=true).
func (m *Memory) EnqueueJob(githubID int64, kind, lane, now string) (Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.GithubID == githubID && (j.Status == StatusQueued || j.Status == StatusRunning) {
			return m.withPosition(j), true, nil
		}
	}
	j := Job{
		ID:         m.nextJob,
		GithubID:   githubID,
		Kind:       kind,
		Lane:       lane,
		Status:     StatusQueued,
		EnqueuedAt: now,
	}
	m.nextJob++
	m.jobs[j.ID] = j
	return m.withPosition(j), false, nil
}

func (m *Memory) QueuedJobs() ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for _, j := range m.jobs {
		if j.Status == StatusQueued {
			out = append(out, j)
		}
	}
	return out, nil
}

func (m *Memory) RunningJobs() ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for _, j := range m.jobs {
		if j.Status == StatusRunning {
			out = append(out, j)
		}
	}
	return out, nil
}

// ClaimJob transitions queued→running atomically (under the mutex here; a
// conditional UPDATE ... WHERE status='queued' in D1).
func (m *Memory) ClaimJob(jobID int64, instance, now string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok || j.Status != StatusQueued {
		return false, nil
	}
	j.Status = StatusRunning
	j.Instance = instance
	j.StartedAt = now
	j.Heartbeat = now
	m.jobs[jobID] = j
	return true, nil
}

func (m *Memory) ReclaimStale(before, now string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, j := range m.jobs {
		if j.Status == StatusRunning && j.Heartbeat != "" && j.Heartbeat < before {
			j.Status = StatusFailed
			j.FinishedAt = now
			j.Error = "reclaimed: instance heartbeat stale"
			m.jobs[id] = j
			n++
		}
	}
	return n, nil
}

func (m *Memory) Heartbeat(jobID int64, now string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return ErrNotFound
	}
	j.Heartbeat = now
	m.jobs[jobID] = j
	return nil
}

// withPosition computes the 1-based place in line for a queued job, honouring
// the fairness order (first-time before refresh, then FIFO) within the lane.
// Caller holds m.mu.
func (m *Memory) withPosition(j Job) Job {
	if j.Status != StatusQueued {
		j.Position = 0
		return j
	}
	pos := 1
	for _, o := range m.jobs {
		if o.Status != StatusQueued || o.Lane != j.Lane || o.ID == j.ID {
			continue
		}
		if lessJob(o, j) {
			pos++
		}
	}
	j.Position = pos
	return j
}

// lessJob is the fairness order: first-time analyses ahead of refreshes, then
// oldest-enqueued first, then by id for determinism.
func lessJob(a, b Job) bool {
	if a.Kind != b.Kind {
		return a.Kind == KindFirst // first ahead of refresh
	}
	if a.EnqueuedAt != b.EnqueuedAt {
		return a.EnqueuedAt < b.EnqueuedAt
	}
	return a.ID < b.ID
}

func (m *Memory) FinishJob(jobID int64, status, finishedAt string, repos, commits, seconds int, jobErr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return ErrNotFound
	}
	j.Status = status
	j.FinishedAt = finishedAt
	j.Repos = repos
	j.Commits = commits
	j.Seconds = seconds
	j.Error = jobErr
	m.jobs[jobID] = j
	return nil
}

func (m *Memory) GetJob(jobID int64) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return Job{}, ErrNotFound
	}
	return m.withPosition(j), nil
}

func (m *Memory) LatestJob(githubID int64) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest Job
	found := false
	for _, j := range m.jobs {
		if j.GithubID != githubID {
			continue
		}
		if !found || j.ID > latest.ID {
			latest, found = j, true
		}
	}
	if !found {
		return Job{}, ErrNotFound
	}
	return m.withPosition(latest), nil
}

func (m *Memory) GetRepoStates(githubID int64) (map[string]RepoState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]RepoState{}
	for k, v := range m.repos[githubID] {
		out[k] = v
	}
	return out, nil
}

func (m *Memory) SaveRepoStates(states []RepoState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range states {
		if m.repos[s.GithubID] == nil {
			m.repos[s.GithubID] = map[string]RepoState{}
		}
		m.repos[s.GithubID][s.FullName] = s
	}
	return nil
}

func (m *Memory) SaveBadge(b Badge) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b.BadgeID == "" {
		return fmt.Errorf("badge id required")
	}
	m.badges[b.BadgeID] = b
	return nil
}

func (m *Memory) ListBadges(githubID int64) ([]Badge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Badge
	for _, b := range m.badges {
		if b.GithubID == githubID {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (m *Memory) GetBadge(badgeID string) (Badge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.badges[badgeID]
	if !ok {
		return Badge{}, ErrNotFound
	}
	return b, nil
}

func (m *Memory) DeleteBadge(badgeID string, githubID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.badges[badgeID]; ok && b.GithubID == githubID {
		delete(m.badges, badgeID)
	}
	return nil
}

// eqFold is a tiny ASCII case-insensitive compare (logins are ASCII).
func eqFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
