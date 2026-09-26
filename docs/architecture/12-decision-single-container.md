# 12 — Decision: one Go container is the whole backend (supersedes parts of 01 and 03)

Owner direction (2026-09-25): "web page, GitHub app with container, all in a single container backend with
wrangler, UI showing dashboard, show the stats here, and let them take a badge, whatever they want."

## Decision

**One Go service in one Cloudflare Container does everything**: web UI (landing, dashboard, leaderboard, /state,
methodology), GitHub App sign-in and API, analysis jobs, rendering, R2 uploads, and Iceberg/DuckDB.
Wrangler deploys it. In front sits the smallest possible Worker. Cloudflare Containers can't take traffic
without one.

```
browser / GitHub camo
        │
        ▼
Front Worker (~30 lines, routing only)
   ├── GET /badge/* /card/* /og/*  ──► R2 agentstats-cards (stream + edge cache; no container wake)
   └── everything else             ──► Container "agentstats" (Go) ──► D1 via HTTP API · R2 · Iceberg · github.com
```

The image routes stay on the Worker → R2 path because a README badge must never wait on a container cold start.
Everything else goes to Go.

## What changes vs docs 01 and 03

| before | now |
|---|---|
| Rust Worker renders pages (askama) | Go renders pages (`html/template`), plain CSS, htmx for small interactions |
| Durable Object `UserJob` state machine | Go in-process job queue + a `jobs` table for state; single-flight per user in Go |
| Worker handles OAuth callback | Go handles `/auth/*` |
| Worker reads D1 via binding | Go reads D1 through the D1 HTTP API (or the Worker exposes a tiny internal D1 proxy route) |
| Two languages in the request path | One: Go. The front Worker is the only other code |

Container instances: `max_instances` 3–5; one warm instance via `sleepAfter` of about 30 min keeps the dashboard fast.
Jobs run in goroutines with per-job scratch dirs. A second instance picking up the same user's job is prevented
by a row lock in `jobs` (status=running, instance id, heartbeat).

## Front Worker language

Owner rule: no TypeScript. Write the front Worker in **Rust (workers-rs)** if its Containers binding covers
`container.fetch`. Otherwise use a ~30-line **plain JavaScript** module (not TypeScript, no build step). Check
workers-rs Containers support first and record which one was used here.

## Badge builder ("whatever they want")

Dashboard → **Make a badge**: the user picks
- **metric**: agent-era lines, multiplier (after ÷ before pace), peak month, agent-signed %, top tool / model,
  top language now, leaderboard rank, total honest lines, commits since agents
- **style**: flat, flat-square, for-the-badge, card-mini (2 lines), full card 1, full card 2
- **theme**: light, dark, deemwar, transparent; custom label text and accent colour
- **period**: all time, this year, last 90 days

A live preview renders server-side (htmx swaps the SVG). On save, Go renders it to
`u/<login>/badges/<badge_id>.svg` (+ PNG) in R2 and shows the Markdown, HTML and URL snippets with copy buttons.
Each badge is refreshed on every re-analysis. Every badge carries the small "agentstats by deemwar" mark and links to `/u/<login>`.

`badges` table (D1): `badge_id, github_id, metric, style, theme, label, accent, period, created_at`.

## Dashboard ("show the stats here")

`/dashboard` for signed-in users, and the same view at the public `/u/:login`:
- Card 1 and Card 2 inline (live SVG, not screenshots)
- Monthly chart with AI milestone ticks, per-tool attribution, language shift
- Excluded-commits table ("why these lines don't count")
- Badge builder + my badges, leaderboard rank, refresh button with job progress
