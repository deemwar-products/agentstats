package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// D1 is a Store backed by the front Worker's internal D1 route.
//
// Per doc 13 fix 1 the container must NOT call the Cloudflare D1 REST API
// (account-wide rate limit). It POSTs SQL to the Worker's `/_internal/d1`
// route, which runs the query with the D1 *binding* (no REST limit) and is
// guarded by a shared secret only the container knows. See worker/index.js.
//
// Config comes from the container env (never hardcoded):
//
//	INTERNAL_D1_URL     e.g. https://agentstats.example.workers.dev/_internal/d1
//	INTERNAL_D1_SECRET  the shared secret sent as X-Internal-Secret
//
// Hot reads should go through Cache (cache.go), which wraps any Store.
type D1 struct {
	url    string
	secret string
	http   *http.Client
}

var _ Store = (*D1)(nil)

// NewD1FromEnv builds a D1 store from the environment, or (nil,false) when the
// internal route is not configured (local/dev — use NewMemory instead).
func NewD1FromEnv() (*D1, bool) {
	url := os.Getenv("INTERNAL_D1_URL")
	secret := os.Getenv("INTERNAL_D1_SECRET")
	if url == "" || secret == "" {
		return nil, false
	}
	return &D1{url: url, secret: secret, http: &http.Client{Timeout: 15 * time.Second}}, true
}

// d1Request is the body POSTed to the Worker's internal route.
type d1Request struct {
	SQL    string `json:"sql"`
	Params []any  `json:"params"`
}

// d1Response mirrors the D1 binding's `.all()` result the Worker forwards.
type d1Response struct {
	Success bool             `json:"success"`
	Results []map[string]any `json:"results"`
	Error   string           `json:"error,omitempty"`
}

func (d *D1) query(sql string, params ...any) ([]map[string]any, error) {
	if params == nil {
		params = []any{}
	}
	body, _ := json.Marshal(d1Request{SQL: sql, Params: params})
	req, err := http.NewRequest(http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// The shared secret authenticates the container to the Worker's internal
	// route. It is read from env, never logged, never in the URL.
	req.Header.Set("X-Internal-Secret", d.secret)
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("internal d1 route: status %d", resp.StatusCode)
	}
	var out d1Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.Success {
		return nil, fmt.Errorf("d1 query failed: %s", out.Error)
	}
	return out.Results, nil
}

func (d *D1) exec(sql string, params ...any) error {
	_, err := d.query(sql, params...)
	return err
}

// ---- Users ----

func (d *D1) UpsertUser(u User) error {
	excluded, _ := json.Marshal(u.ExcludedRepos)
	return d.exec(`INSERT INTO users (github_id, login, name, avatar_url, agent_start, is_public, excluded_repos, notify_email, last_login_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
ON CONFLICT(github_id) DO UPDATE SET
  login=excluded.login, name=excluded.name, avatar_url=excluded.avatar_url,
  last_login_at=datetime('now')`,
		u.GithubID, u.Login, u.Name, u.AvatarURL, orDefault(u.AgentStart, "auto"),
		boolToInt(u.IsPublic), string(excluded), boolToInt(u.NotifyEmail))
}

func (d *D1) GetUserByLogin(login string) (User, error) {
	rows, err := d.query(`SELECT github_id, login, name, avatar_url, agent_start, is_public, excluded_repos, notify_email, leaderboard, created_at, last_login_at FROM users WHERE login = ? COLLATE NOCASE`, login)
	if err != nil {
		return User{}, err
	}
	if len(rows) == 0 {
		return User{}, ErrNotFound
	}
	return userFromRow(rows[0]), nil
}

func (d *D1) GetUserByID(githubID int64) (User, error) {
	rows, err := d.query(`SELECT github_id, login, name, avatar_url, agent_start, is_public, excluded_repos, notify_email, leaderboard, created_at, last_login_at FROM users WHERE github_id = ?`, githubID)
	if err != nil {
		return User{}, err
	}
	if len(rows) == 0 {
		return User{}, ErrNotFound
	}
	return userFromRow(rows[0]), nil
}

func (d *D1) UpdateSettings(githubID int64, agentStart string, isPublic bool, excludedRepos []string, notifyEmail, leaderboard bool) error {
	excluded, _ := json.Marshal(orEmpty(excludedRepos))
	return d.exec(`UPDATE users SET agent_start=?, is_public=?, excluded_repos=?, notify_email=?, leaderboard=? WHERE github_id=?`,
		agentStart, boolToInt(isPublic), string(excluded), boolToInt(notifyEmail), boolToInt(leaderboard), githubID)
}

func (d *D1) DeleteUser(githubID int64) error {
	// stats/jobs/badges/repo_state cascade via ON DELETE CASCADE (see migrations).
	return d.exec(`DELETE FROM users WHERE github_id = ?`, githubID)
}

// ---- Stats ----

func (d *D1) SaveStats(s StatsRow) error {
	return d.exec(`INSERT INTO stats (github_id, version, computed_at, stats_json, headline)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(github_id) DO UPDATE SET version=excluded.version, computed_at=excluded.computed_at,
  stats_json=excluded.stats_json, headline=excluded.headline`,
		s.GithubID, s.Version, orDefault(s.ComputedAt, nowUTC()), s.StatsJSON, s.Headline)
}

func (d *D1) GetStats(githubID int64) (StatsRow, error) {
	rows, err := d.query(`SELECT github_id, version, computed_at, stats_json, headline FROM stats WHERE github_id = ?`, githubID)
	if err != nil {
		return StatsRow{}, err
	}
	if len(rows) == 0 {
		return StatsRow{}, ErrNotFound
	}
	r := rows[0]
	return StatsRow{
		GithubID:   getInt64(r, "github_id"),
		Version:    int(getInt64(r, "version")),
		ComputedAt: getString(r, "computed_at"),
		StatsJSON:  getString(r, "stats_json"),
		Headline:   getString(r, "headline"),
	}, nil
}

// LeaderboardRows joins public, opted-in users with their stats. The INSERT in
// UpsertUser leaves `leaderboard` to its DB default (1), so new users are on.
func (d *D1) LeaderboardRows() ([]LeaderboardRow, error) {
	rows, err := d.query(`SELECT u.github_id, u.login, u.name, u.avatar_url, u.agent_start, u.is_public, u.leaderboard,
  s.version, s.computed_at, s.stats_json, s.headline
FROM users u JOIN stats s ON s.github_id = u.github_id
WHERE u.is_public = 1 AND u.leaderboard = 1`)
	if err != nil {
		return nil, err
	}
	out := make([]LeaderboardRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, LeaderboardRow{
			User: User{
				GithubID:    getInt64(r, "github_id"),
				Login:       getString(r, "login"),
				Name:        getString(r, "name"),
				AvatarURL:   getString(r, "avatar_url"),
				AgentStart:  getString(r, "agent_start"),
				IsPublic:    getInt64(r, "is_public") != 0,
				Leaderboard: getInt64(r, "leaderboard") != 0,
			},
			Stats: StatsRow{
				GithubID:   getInt64(r, "github_id"),
				Version:    int(getInt64(r, "version")),
				ComputedAt: getString(r, "computed_at"),
				StatsJSON:  getString(r, "stats_json"),
				Headline:   getString(r, "headline"),
			},
		})
	}
	return out, nil
}

// ---- Jobs (queue) ----

func (d *D1) EnqueueJob(githubID int64, kind, lane, now string) (Job, bool, error) {
	rows, err := d.query(`SELECT id, github_id, kind, lane, status, enqueued_at, started_at, finished_at, instance, heartbeat, repos, commits, seconds, error FROM jobs WHERE github_id=? AND status IN ('queued','running') LIMIT 1`, githubID)
	if err != nil {
		return Job{}, false, err
	}
	if len(rows) > 0 {
		j := jobFromRow(rows[0])
		pos, _ := d.position(j)
		j.Position = pos
		return j, true, nil
	}
	ins, err := d.query(`INSERT INTO jobs (github_id, kind, lane, status, enqueued_at) VALUES (?, ?, ?, 'queued', ?) RETURNING id`, githubID, kind, lane, now)
	if err != nil {
		return Job{}, false, err
	}
	var id int64
	if len(ins) > 0 {
		id = getInt64(ins[0], "id")
	}
	j := Job{ID: id, GithubID: githubID, Kind: kind, Lane: lane, Status: StatusQueued, EnqueuedAt: now}
	pos, _ := d.position(j)
	j.Position = pos
	return j, false, nil
}

// position counts queued jobs in the same lane that rank ahead of j.
func (d *D1) position(j Job) (int, error) {
	if j.Status != StatusQueued {
		return 0, nil
	}
	// first-time ahead of refresh, then oldest enqueued, then lowest id.
	rows, err := d.query(`SELECT COUNT(*) AS c FROM jobs WHERE status='queued' AND lane=? AND (
	  (kind='first' AND ?='refresh') OR
	  (kind=? AND (enqueued_at < ? OR (enqueued_at = ? AND id < ?)))
	)`, j.Lane, j.Kind, j.Kind, j.EnqueuedAt, j.EnqueuedAt, j.ID)
	if err != nil {
		return 0, err
	}
	ahead := 0
	if len(rows) > 0 {
		ahead = int(getInt64(rows[0], "c"))
	}
	return ahead + 1, nil
}

func (d *D1) QueuedJobs() ([]Job, error)  { return d.listJobsByStatus(StatusQueued) }
func (d *D1) RunningJobs() ([]Job, error) { return d.listJobsByStatus(StatusRunning) }

func (d *D1) listJobsByStatus(status string) ([]Job, error) {
	rows, err := d.query(`SELECT id, github_id, kind, lane, status, enqueued_at, started_at, finished_at, instance, heartbeat, repos, commits, seconds, error FROM jobs WHERE status=?`, status)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, jobFromRow(r))
	}
	return out, nil
}

func (d *D1) ClaimJob(jobID int64, instance, now string) (bool, error) {
	// Conditional update = the atomic claim. If another instance already claimed
	// it, changes=0. We re-read to confirm this instance won.
	if err := d.exec(`UPDATE jobs SET status='running', instance=?, started_at=?, heartbeat=? WHERE id=? AND status='queued'`,
		instance, now, now, jobID); err != nil {
		return false, err
	}
	rows, err := d.query(`SELECT instance, status FROM jobs WHERE id=?`, jobID)
	if err != nil || len(rows) == 0 {
		return false, err
	}
	return getString(rows[0], "status") == StatusRunning && getString(rows[0], "instance") == instance, nil
}

func (d *D1) Heartbeat(jobID int64, now string) error {
	return d.exec(`UPDATE jobs SET heartbeat=? WHERE id=?`, now, jobID)
}

func (d *D1) FinishJob(jobID int64, status, finishedAt string, repos, commits, seconds int, jobErr string) error {
	return d.exec(`UPDATE jobs SET status=?, finished_at=?, repos=?, commits=?, seconds=?, error=? WHERE id=?`,
		status, finishedAt, repos, commits, seconds, jobErr, jobID)
}

func (d *D1) ReclaimStale(before, now string) (int, error) {
	if err := d.exec(`UPDATE jobs SET status='failed', finished_at=?, error='reclaimed: instance heartbeat stale' WHERE status='running' AND heartbeat < ?`, now, before); err != nil {
		return 0, err
	}
	return 0, nil // D1 changes count is not surfaced through the internal route
}

func (d *D1) GetJob(jobID int64) (Job, error) {
	rows, err := d.query(`SELECT id, github_id, kind, lane, status, enqueued_at, started_at, finished_at, instance, heartbeat, repos, commits, seconds, error FROM jobs WHERE id=?`, jobID)
	if err != nil {
		return Job{}, err
	}
	if len(rows) == 0 {
		return Job{}, ErrNotFound
	}
	j := jobFromRow(rows[0])
	j.Position, _ = d.position(j)
	return j, nil
}

func (d *D1) LatestJob(githubID int64) (Job, error) {
	rows, err := d.query(`SELECT id, github_id, kind, lane, status, enqueued_at, started_at, finished_at, instance, heartbeat, repos, commits, seconds, error FROM jobs WHERE github_id=? ORDER BY id DESC LIMIT 1`, githubID)
	if err != nil {
		return Job{}, err
	}
	if len(rows) == 0 {
		return Job{}, ErrNotFound
	}
	j := jobFromRow(rows[0])
	j.Position, _ = d.position(j)
	return j, nil
}

// ---- Badges ----

func (d *D1) SaveBadge(b Badge) error {
	return d.exec(`INSERT INTO badges (badge_id, github_id, metric, style, theme, label, accent, period, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(badge_id) DO UPDATE SET metric=excluded.metric, style=excluded.style, theme=excluded.theme,
  label=excluded.label, accent=excluded.accent, period=excluded.period`,
		b.BadgeID, b.GithubID, b.Metric, b.Style, b.Theme, b.Label, b.Accent, b.Period, orDefault(b.CreatedAt, nowUTC()))
}

func (d *D1) ListBadges(githubID int64) ([]Badge, error) {
	rows, err := d.query(`SELECT badge_id, github_id, metric, style, theme, label, accent, period, created_at FROM badges WHERE github_id=? ORDER BY created_at`, githubID)
	if err != nil {
		return nil, err
	}
	out := make([]Badge, 0, len(rows))
	for _, r := range rows {
		out = append(out, badgeFromRow(r))
	}
	return out, nil
}

func (d *D1) GetBadge(badgeID string) (Badge, error) {
	rows, err := d.query(`SELECT badge_id, github_id, metric, style, theme, label, accent, period, created_at FROM badges WHERE badge_id=?`, badgeID)
	if err != nil {
		return Badge{}, err
	}
	if len(rows) == 0 {
		return Badge{}, ErrNotFound
	}
	return badgeFromRow(rows[0]), nil
}

func (d *D1) DeleteBadge(badgeID string, githubID int64) error {
	return d.exec(`DELETE FROM badges WHERE badge_id=? AND github_id=?`, badgeID, githubID)
}

// ---- repo_state ----

func (d *D1) GetRepoStates(githubID int64) (map[string]RepoState, error) {
	rows, err := d.query(`SELECT github_id, full_name, last_seen_sha, pushed_at FROM repo_state WHERE github_id=?`, githubID)
	if err != nil {
		return nil, err
	}
	out := map[string]RepoState{}
	for _, r := range rows {
		fn := getString(r, "full_name")
		out[fn] = RepoState{
			GithubID:    getInt64(r, "github_id"),
			FullName:    fn,
			LastSeenSHA: getString(r, "last_seen_sha"),
			PushedAt:    getString(r, "pushed_at"),
		}
	}
	return out, nil
}

func (d *D1) SaveRepoStates(states []RepoState) error {
	for _, s := range states {
		if err := d.exec(`INSERT INTO repo_state (github_id, full_name, last_seen_sha, pushed_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(github_id, full_name) DO UPDATE SET last_seen_sha=excluded.last_seen_sha, pushed_at=excluded.pushed_at`,
			s.GithubID, s.FullName, s.LastSeenSHA, s.PushedAt); err != nil {
			return err
		}
	}
	return nil
}

// ---- row mapping helpers ----

func userFromRow(r map[string]any) User {
	var excluded []string
	if raw := getString(r, "excluded_repos"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &excluded)
	}
	return User{
		GithubID:      getInt64(r, "github_id"),
		Login:         getString(r, "login"),
		Name:          getString(r, "name"),
		AvatarURL:     getString(r, "avatar_url"),
		AgentStart:    getString(r, "agent_start"),
		IsPublic:      getInt64(r, "is_public") != 0,
		ExcludedRepos: orEmpty(excluded),
		NotifyEmail:   getInt64(r, "notify_email") != 0,
		Leaderboard:   getInt64(r, "leaderboard") != 0,
		CreatedAt:     getString(r, "created_at"),
		LastLoginAt:   getString(r, "last_login_at"),
	}
}

func jobFromRow(r map[string]any) Job {
	return Job{
		ID:         getInt64(r, "id"),
		GithubID:   getInt64(r, "github_id"),
		Kind:       getString(r, "kind"),
		Lane:       getString(r, "lane"),
		Status:     getString(r, "status"),
		EnqueuedAt: getString(r, "enqueued_at"),
		StartedAt:  getString(r, "started_at"),
		FinishedAt: getString(r, "finished_at"),
		Instance:   getString(r, "instance"),
		Heartbeat:  getString(r, "heartbeat"),
		Repos:      int(getInt64(r, "repos")),
		Commits:    int(getInt64(r, "commits")),
		Seconds:    int(getInt64(r, "seconds")),
		Error:      getString(r, "error"),
	}
}

func badgeFromRow(r map[string]any) Badge {
	return Badge{
		BadgeID:   getString(r, "badge_id"),
		GithubID:  getInt64(r, "github_id"),
		Metric:    getString(r, "metric"),
		Style:     getString(r, "style"),
		Theme:     getString(r, "theme"),
		Label:     getString(r, "label"),
		Accent:    getString(r, "accent"),
		Period:    getString(r, "period"),
		CreatedAt: getString(r, "created_at"),
	}
}

func getString(r map[string]any, k string) string {
	v, ok := r[k]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// getInt64 handles JSON numbers (float64) and numeric strings from D1.
func getInt64(r map[string]any, k string) int64 {
	v, ok := r[k]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		var i int64
		_, _ = fmt.Sscan(strings.TrimSpace(n), &i)
		return i
	}
	return 0
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nowUTC() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }
