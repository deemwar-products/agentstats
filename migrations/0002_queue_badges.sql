-- agentstats D1 — doc 12 (single container) + doc 13 (visible/fair queue,
-- incremental refresh). Aggregates + operational cursors only; never code,
-- tokens or verified emails. See docs/architecture/04, 12, 13.

-- users: opt-in email-when-done (doc 13).
ALTER TABLE users ADD COLUMN notify_email INTEGER NOT NULL DEFAULT 0;

-- jobs becomes the QUEUE: queued -> running -> done|failed, with fairness
-- (kind: first before refresh; lane: big accounts separated) and a single-flight
-- lock (instance + heartbeat). Recreated because SQLite can't retype `status`.
DROP TABLE IF EXISTS jobs;
CREATE TABLE jobs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  github_id    INTEGER NOT NULL,
  kind         TEXT NOT NULL DEFAULT 'first',   -- first | refresh
  lane         TEXT NOT NULL DEFAULT 'normal',  -- normal | big (>300 repos)
  status       TEXT NOT NULL DEFAULT 'queued',  -- queued | running | done | failed
  enqueued_at  TEXT NOT NULL,
  started_at   TEXT,
  finished_at  TEXT,
  instance     TEXT,                            -- container instance holding the running row
  heartbeat    TEXT,                            -- last heartbeat; stale rows are reclaimed
  repos        INTEGER, commits INTEGER, seconds INTEGER, error TEXT
);
CREATE INDEX jobs_user   ON jobs(github_id, id);
CREATE INDEX jobs_status ON jobs(status, lane, kind, enqueued_at);

-- badges: the badge builder (doc 12).
CREATE TABLE IF NOT EXISTS badges (
  badge_id   TEXT PRIMARY KEY,
  github_id  INTEGER NOT NULL REFERENCES users(github_id) ON DELETE CASCADE,
  metric     TEXT NOT NULL,
  style      TEXT NOT NULL,
  theme      TEXT NOT NULL,
  label      TEXT,
  accent     TEXT,
  period     TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS badges_user ON badges(github_id);

-- repo_state: incremental-refresh cursors (doc 13 fix 3). Operational metadata,
-- never rendered publicly. Skips repos whose pushed_at is unchanged on refresh.
CREATE TABLE IF NOT EXISTS repo_state (
  github_id     INTEGER NOT NULL REFERENCES users(github_id) ON DELETE CASCADE,
  full_name     TEXT NOT NULL,
  last_seen_sha TEXT,
  pushed_at     TEXT,
  PRIMARY KEY (github_id, full_name)
);
