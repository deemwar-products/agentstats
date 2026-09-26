# 10 — Data lake (Iceberg on R2 + DuckDB)

Beyond the per-user profile, agentstats keeps an append-only **data lake** of aggregate stats so we can
publish a cohort-level "State of agent coding" view and answer cross-user questions cheaply. Aggregates
only — never code, never per-commit content, never tokens.

## Storage: Iceberg tables in R2

- **Bucket:** `agentstats-lake` (separate from `agentstats-cards`), exposed through the **R2 Data
  Catalog** (Iceberg REST catalog) so tables are queryable by any Iceberg/DuckDB client.
- The **Go container** is the only writer. At the end of a job it appends the job's aggregates using
  **`iceberg-go`** against the R2 Data Catalog (catalog URI + R2 credentials from secrets).
- Reads/rollups use **DuckDB** (`go-duckdb`) with the `iceberg` and `httpfs` extensions pointed at the
  same catalog.

## Tables (all aggregates)

| table | grain | columns (sketch) |
|---|---|---|
| `user_month` | login × month | lines, commits, repos_touched, era (`before`/`after`) |
| `user_month_lang` | login × month × language | lines |
| `user_month_agent` | login × month × tool | signed_commits, signed_lines, models(map) |
| `jobs` | job | login, started_at, finished_at, version, status, dropped_count |
| `milestones` | milestone | date, tool, event, kind, source_url (mirrors `analyzer/milestones.json`) |

`login` is stored as a stable hash where a cell is published publicly; raw login stays only where the
user has a public profile.

## Rollups → public `/state` page

- A **daily DuckDB rollup** job aggregates the lake into small JSON/SVG artifacts written to
  `agentstats-cards` (e.g. `state/YYYY-MM-DD/…`), which the Worker serves like any other card.
- **Cohort floor: a published cell must cover ≥ 50 distinct users.** Anything thinner is suppressed
  (shown as "—"), so no individual is re-identifiable from an aggregate.
- The `/state` "State of agent coding" page renders these: adoption over time, lines/commit shift,
  language mix shift, per-tool share — all cohort-level.

## What stays in D1

D1 (`agentstats`) remains the **per-user profile read path** — the fast lookup behind `/u/:login`
(current version pointer, settings, latest aggregates). The lake is the analytics/history layer; D1 is
the serving layer. They are written from the same end-of-job aggregates.

## Deploy additions (see LAUNCH.md)

- Enable **R2 Data Catalog** on the account and create bucket `agentstats-lake` + a scoped R2 token.
- The container image bundles the DuckDB extensions (or fetches them at build, pinned versions).
- Daily rollup runs as a scheduled Worker (cron trigger) that invokes the container's rollup mode.
