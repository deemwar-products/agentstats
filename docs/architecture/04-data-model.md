# 04 — Data model

## D1 `agentstats`

```sql
CREATE TABLE users (
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

CREATE TABLE stats (
  github_id    INTEGER PRIMARY KEY REFERENCES users(github_id) ON DELETE CASCADE,
  version      INTEGER NOT NULL,                     -- matches R2 u/<login>/v<n>/
  computed_at  TEXT NOT NULL,
  stats_json   TEXT NOT NULL,                        -- Stats below (aggregates only)
  headline     TEXT NOT NULL                         -- precomputed for <title>/og
);

CREATE TABLE jobs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  github_id    INTEGER NOT NULL,
  started_at   TEXT NOT NULL,
  finished_at  TEXT,
  status       TEXT NOT NULL,                        -- done | failed | skipped
  repos        INTEGER, commits INTEGER, seconds INTEGER, error TEXT
);
CREATE INDEX jobs_user ON jobs(github_id, started_at);
```

No tokens, emails or code in D1. Emails are passed to the analyzer per job only.

## `Stats` JSON (analyzer output, rendered into cards)

```json
{
  "login": "muthuishere", "agent_start": "2025-01", "first_year": "2013",
  "totals": { "lines": 1947874, "commits": 5610, "repos": 359 },
  "eras": {
    "before": { "from": "2013", "to": "2024", "lines": 696135, "commits": 1814, "repos_touched": 162,
                "lines_per_commit": 384, "langs": {"JavaScript": 227212, "Java": 168482}, "daily_langs": 4,
                "pace_per_year": 58011 },
    "after":  { "from": "2025", "to": "2026", "lines": 1251739, "commits": 3796, "repos_touched": 69,
                "lines_per_commit": 330, "langs": {"Go": 573260}, "daily_langs": 12, "pace_this_year": 1128412 }
  },
  "years":  { "2013": {"lines": 10441, "commits": 22} },
  "months": { "2025-01": 1234 },
  "peak_month": { "month": "2026-07", "lines": 567485, "beats_best_year": "2019" },
  "excluded": { "commits": 32, "lines": 4100000,
                "top": [{"repo": "vscode-agent", "sha": "ecb85df8", "files": 16239, "lines": 3259905, "reason": "bulk-import"}] },
  "skipped_repos": [{"repo": "o/huge", "reason": "size>1GB"}]
}
```

Example numbers are illustrative. `daily_langs` = languages with ≥ 2 % of the era's lines.

## R2 `agentstats-cards` layout

```
u/<login>/current.json                 { "v": 7 }
u/<login>/v7/badge.svg
u/<login>/v7/before-after.svg|png
u/<login>/v7/composition.svg|png
u/<login>/v7/og.png                    1200×630
```
Keep the last 2 versions; delete older ones on upload. Delete-my-data removes the whole `u/<login>/` prefix.
