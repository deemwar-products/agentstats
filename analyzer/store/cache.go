package store

import (
	"sync"
	"time"
)

// Cache wraps a Store with an in-memory hot-read cache (doc 13 fix 1: profile +
// stats JSON ~60s). It keeps dashboards fast and shields the backing Store from
// read storms during a traffic spike. Writes invalidate the affected keys.
//
// Only reads that dominate under load are cached (user-by-login, user-by-id,
// stats). Job/queue reads are intentionally uncached — they must be live.
type Cache struct {
	inner Store
	ttl   time.Duration

	mu      sync.RWMutex
	userLg  map[string]cachedUser  // login -> user
	userID  map[int64]cachedUser   // id -> user
	stats   map[int64]cachedStats  // id -> stats
	now     func() time.Time       // injectable clock for tests
}

type cachedUser struct {
	u   User
	exp time.Time
}
type cachedStats struct {
	s   StatsRow
	exp time.Time
}

// NewCache wraps inner with a TTL (use 60s per doc 13; <=0 disables caching).
func NewCache(inner Store, ttl time.Duration) *Cache {
	return &Cache{
		inner:  inner,
		ttl:    ttl,
		userLg: map[string]cachedUser{},
		userID: map[int64]cachedUser{},
		stats:  map[int64]cachedStats{},
		now:    time.Now,
	}
}

var _ Store = (*Cache)(nil)

func (c *Cache) live() bool { return c.ttl > 0 }

func (c *Cache) GetUserByLogin(login string) (User, error) {
	if c.live() {
		c.mu.RLock()
		if e, ok := c.userLg[lower(login)]; ok && c.now().Before(e.exp) {
			c.mu.RUnlock()
			return e.u, nil
		}
		c.mu.RUnlock()
	}
	u, err := c.inner.GetUserByLogin(login)
	if err == nil && c.live() {
		c.putUser(u)
	}
	return u, err
}

func (c *Cache) GetUserByID(githubID int64) (User, error) {
	if c.live() {
		c.mu.RLock()
		if e, ok := c.userID[githubID]; ok && c.now().Before(e.exp) {
			c.mu.RUnlock()
			return e.u, nil
		}
		c.mu.RUnlock()
	}
	u, err := c.inner.GetUserByID(githubID)
	if err == nil && c.live() {
		c.putUser(u)
	}
	return u, err
}

func (c *Cache) GetStats(githubID int64) (StatsRow, error) {
	if c.live() {
		c.mu.RLock()
		if e, ok := c.stats[githubID]; ok && c.now().Before(e.exp) {
			c.mu.RUnlock()
			return e.s, nil
		}
		c.mu.RUnlock()
	}
	s, err := c.inner.GetStats(githubID)
	if err == nil && c.live() {
		c.mu.Lock()
		c.stats[githubID] = cachedStats{s: s, exp: c.now().Add(c.ttl)}
		c.mu.Unlock()
	}
	return s, err
}

func (c *Cache) putUser(u User) {
	c.mu.Lock()
	e := cachedUser{u: u, exp: c.now().Add(c.ttl)}
	c.userLg[lower(u.Login)] = e
	c.userID[u.GithubID] = e
	c.mu.Unlock()
}

func (c *Cache) invalidateUser(githubID int64, login string) {
	c.mu.Lock()
	delete(c.userID, githubID)
	if login != "" {
		delete(c.userLg, lower(login))
	}
	delete(c.stats, githubID)
	c.mu.Unlock()
}

// ---- writes: invalidate then delegate ----

func (c *Cache) UpsertUser(u User) error {
	c.invalidateUser(u.GithubID, u.Login)
	return c.inner.UpsertUser(u)
}

func (c *Cache) UpdateSettings(githubID int64, agentStart string, isPublic bool, excludedRepos []string, notifyEmail, leaderboard bool) error {
	c.invalidateUser(githubID, "")
	return c.inner.UpdateSettings(githubID, agentStart, isPublic, excludedRepos, notifyEmail, leaderboard)
}

func (c *Cache) DeleteUser(githubID int64) error {
	c.invalidateUser(githubID, "")
	return c.inner.DeleteUser(githubID)
}

func (c *Cache) SaveStats(s StatsRow) error {
	c.mu.Lock()
	delete(c.stats, s.GithubID)
	c.mu.Unlock()
	return c.inner.SaveStats(s)
}

// ---- pass-through (uncached) ----

// LeaderboardRows is uncached here; the web layer caches the computed board.
func (c *Cache) LeaderboardRows() ([]LeaderboardRow, error) { return c.inner.LeaderboardRows() }

func (c *Cache) EnqueueJob(g int64, kind, lane, now string) (Job, bool, error) {
	return c.inner.EnqueueJob(g, kind, lane, now)
}
func (c *Cache) QueuedJobs() ([]Job, error)  { return c.inner.QueuedJobs() }
func (c *Cache) RunningJobs() ([]Job, error) { return c.inner.RunningJobs() }
func (c *Cache) ClaimJob(id int64, inst, now string) (bool, error) {
	return c.inner.ClaimJob(id, inst, now)
}
func (c *Cache) Heartbeat(id int64, now string) error { return c.inner.Heartbeat(id, now) }
func (c *Cache) FinishJob(id int64, status, fin string, r, cm, s int, e string) error {
	return c.inner.FinishJob(id, status, fin, r, cm, s, e)
}
func (c *Cache) ReclaimStale(before, now string) (int, error) { return c.inner.ReclaimStale(before, now) }
func (c *Cache) GetJob(id int64) (Job, error)                 { return c.inner.GetJob(id) }
func (c *Cache) LatestJob(g int64) (Job, error)               { return c.inner.LatestJob(g) }
func (c *Cache) SaveBadge(b Badge) error                      { return c.inner.SaveBadge(b) }
func (c *Cache) ListBadges(g int64) ([]Badge, error)          { return c.inner.ListBadges(g) }
func (c *Cache) GetBadge(id string) (Badge, error)            { return c.inner.GetBadge(id) }
func (c *Cache) DeleteBadge(id string, g int64) error         { return c.inner.DeleteBadge(id, g) }
func (c *Cache) GetRepoStates(g int64) (map[string]RepoState, error) {
	return c.inner.GetRepoStates(g)
}
func (c *Cache) SaveRepoStates(s []RepoState) error { return c.inner.SaveRepoStates(s) }

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if 'A' <= b[i] && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
