# Phase B — deferred (not built in the single-container Phase A)

Phase A shipped a working single-user path: sign-in → fair job queue → analyze →
render (cards, badge, og.png) → R2 upload (stub) → dashboard with live inline
SVG cards + share. The following are intentionally left as stubs/TODOs so we
ship the working dashboard first.

## 1. Iceberg / DuckDB data lake (doc 10, and doc 13 fix 2)
- Per-job appends cause catalog commit conflicts + tiny-file explosions.
- Plan: jobs write aggregate rows to a staging prefix (`lake-staging/<job_id>.parquet`
  or a D1 staging table); ONE lake writer (single-flight) appends all staged rows
  to Iceberg every 5–10 min in one commit per table; weekly compaction + snapshot
  expiry; physical removal on delete-my-data.
- Status: not started. No lake code in the container.

## 2. Public `/state` global page ("State of agent coding")
- Aggregate charts across all opted-in users. Needs the data lake (1) first.
- Status: not started.

## 3. "N developers analyzed" usage counter
- The landing counter's global numbers. Must be wired to a real aggregate
  (a counter table or the lake), never hardcoded. Landing currently shows the
  SAMPLE card's own figures, labelled as a sample — no fake globals ship.
- Status: stub.

## 4. Opt-in leaderboard (doc 11)
- `/leaderboard` renders a STUB page (Phase B banner) — no rows, no ranks.
- Gating rules (≥2 years + 5K pre-agent lines; outliers hidden until reviewed)
  and the rank metric are designed in doc 11 but not implemented.
- Status: stub page only.

## Also stubbed / owner-gated in Phase A (not Phase B, but not wired here)
- **R2 PutObject / DeletePrefix** (`analyzer/r2/r2.go`): the S3 client is
  structured; the one credentialed call is a TODO stub reading creds from env.
  Wire with aws-sdk-go-v2 at launch. The dashboard renders cards inline without it.
- **GitHub App**: not created (owner manifest-flow step, docs 05). Sign-in returns
  a clear "not configured" until `GITHUB_CLIENT_ID`/`_SECRET` are set; local
  testing uses the dev sign-in (`DEV_AUTH=1`, `/auth/dev?login=you`).
- **Refresh with a fresh token**: a real refresh needs a new user token (not
  stored, docs 05/07); the refresh button re-runs sign-in. Dev mode analyzes a
  local repo glob (`AGENTSTATS_DEV_REPOS`).
- **PNG rasterisation** needs `resvg` (bundled in the container image); locally
  the og/card PNG routes fall back to serving the SVG.
