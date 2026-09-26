# 13 — Load and limits (what breaks when many people arrive at once)

Owner question (2026-09-25): "many people come in, so many requests will not cause issue right?"
Answer: views scale without limit; analysis is the bottleneck. Two parts of doc 12 would fail under a spike and are fixed here.

## Per path

| path | scales? | notes |
|---|---|---|
| `/badge/*`, `/card/*`, `/og/*` | yes, unbounded | Worker → R2 + edge cache. Never wakes the container. Most traffic is here |
| Pages / dashboard | yes | Go serves thousands of req/s per instance; keep one warm (`sleepAfter`), `max_instances` sized for cost |
| Analysis jobs | **bottleneck** | Minutes of CPU/disk/network each. Needs a visible queue (below) |
| D1 access from Go | **fix required** | See fix 1 |
| Iceberg writes | **fix required** | See fix 2 |
| GitHub | mostly | REST limits are per user token (5,000/h each), so they scale with users; mass `git clone` from Cloudflare egress can hit GitHub secondary/abuse limits. See fix 3 |

## Fix 1: no D1 REST API on the request path

The Cloudflare REST API has an account-wide rate limit (order of 1,200 requests / 5 min; confirm the current figure).
Dashboards reading D1 through it would fail under load. Instead:
- The front Worker exposes an **internal** route (`/_internal/d1`, shared-secret header, only reachable from the
  container) that runs the query with the **D1 binding**. Bindings have no REST API limit.
- Go caches hot reads in memory (profile + stats JSON for 60 s; counters for 5 min).

## Fix 2: batch Iceberg writes

Per-job appends from many concurrent jobs cause catalog commit conflicts and tiny-file explosions.
- Jobs write their aggregate rows to a staging prefix in R2 (`lake-staging/<job_id>.parquet`) or a D1 staging table.
- **One** lake writer (single-flight lock) appends all staged rows to Iceberg every 5–10 min in one commit per table.
- Weekly compaction and snapshot expiry (and the physical removal after delete-my-data).

## Fix 3: gentle on GitHub

- **Incremental refresh**: store per repo `last_seen_sha` + the pack's `pushed_at`. On refresh, skip repos with
  unchanged `pushed_at`. For changed ones, a `git fetch` of new commits beats a full re-clone
  (keep a per-user cache in R2 as a git bundle when the size allows; otherwise re-clone only changed repos).
- Clone concurrency cap across all jobs (for example 20 in flight), jittered backoff on 429/403 secondary-limit responses,
  honour `Retry-After`.

## Job queue (visible, fair, cost-capped)

- `jobs` table is the queue: `queued → running → done|failed`, with `position` shown to the user
  ("You're #37 in line, about 12 min. We'll email you, or keep this tab open.").
- Per instance: at most N concurrent jobs (start with 2–3, tuned to instance CPU/disk). Global cap =
  `max_instances × N`. **`max_instances` is the cost ceiling.** Raise it deliberately, not automatically.
- Fairness: first-time analyses before refreshes; big accounts (> 300 repos) go to a separate lane so they don't
  block everyone else.
- Per-job limits from doc 02 (500 repos / 2 GB / 15 min). Check the container instance type's disk and memory
  against the 2 GB clone cap and pick the instance type to match.
- Optional notify-when-done by email (GitHub primary email, only if the user opts in).

## Load test before launch

Synthetic: 1,000 concurrent badge GETs (should all be edge hits), 200 concurrent dashboard loads, and 50 queued jobs
against test accounts. Record p95 and container instance count; set `max_instances` from the result and the
monthly cost at that ceiling.
