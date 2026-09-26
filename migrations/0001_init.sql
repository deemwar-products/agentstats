-- agentstats D1 schema (binding DB). Aggregates only — never code, tokens or emails.
-- See docs/architecture/04-data-model.md.

CREATE TABLE IF NOT EXISTS users (
  github_id      INTEGER PRIMARY KEY,
  login          TEXT NOT NULL UNIQUE COLLATE NOCASE,
  name           TEXT,
  avatar_url     TEXT,
  agent_start    TEXT NOT NULL DEFAULT '2025-01',   -- YYYY-MM
  is_public      INTEGER NOT NULL DEFAULT 1,
  excluded_repos TEXT NOT NULL DEFAULT '[]',        -- JSON array of full_name
  created_at     TEXT NOT NULL DEFAULT (datetime('now')),
  last_login_at  TEXT
);

CREATE TABLE IF NOT EXISTS stats (
  github_id    INTEGER PRIMARY KEY REFERENCES users(github_id) ON DELETE CASCADE,
  version      INTEGER NOT NULL,                     -- matches R2 u/<login>/v<n>/
  computed_at  TEXT NOT NULL,
  stats_json   TEXT NOT NULL,                        -- Stats (aggregates only)
  headline     TEXT NOT NULL                         -- precomputed for <title>/og
);

CREATE TABLE IF NOT EXISTS jobs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  github_id    INTEGER NOT NULL,
  started_at   TEXT NOT NULL,
  finished_at  TEXT,
  status       TEXT NOT NULL,                        -- done | failed | skipped
  repos        INTEGER,
  commits      INTEGER,
  seconds      INTEGER,
  error        TEXT
);

CREATE INDEX IF NOT EXISTS jobs_user ON jobs(github_id, started_at);
