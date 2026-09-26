# agentstats — architecture

Honest "before agents vs after agents" code stats from a developer's GitHub history, rendered as shareable
cards and a README badge. Public product by deemwar at `agentstats.deemwar.com`.

> **Current design: [12-decision-single-container.md](12-decision-single-container.md) — one Go container is the whole backend;** it supersedes the Rust-Worker and Durable Object parts of 01 and 03.

| doc | covers |
|---|---|
| [01-system-overview.md](01-system-overview.md) | Components, request flow, analysis job flow |
| [02-analyzer.md](02-analyzer.md) | Go analyzer in a Cloudflare Container: clone, count, filter, render, upload to R2 |
| [03-worker-api.md](03-worker-api.md) | Rust Worker (workers-rs): routes, Durable Object job control, caching |
| [04-data-model.md](04-data-model.md) | D1 tables and the stats JSON |
| [05-github-app-auth.md](05-github-app-auth.md) | GitHub App, sign-in, token handling |
| [06-cards-and-badges.md](06-cards-and-badges.md) | Card 1, Card 2, badge — layout, data, rendering |
| [07-privacy-security.md](07-privacy-security.md) | What we read, store, discard; abuse limits |
| [08-deploy.md](08-deploy.md) | Cloudflare resources, domain, CI, environments |
| [10-data-lake.md](10-data-lake.md) | R2 + Iceberg (R2 Data Catalog) + DuckDB: per-user history and global "State of agent coding" rollups |
| [11-usage-stats-and-leaderboard.md](11-usage-stats-and-leaderboard.md) | "N developers analyzed" counter, opt-in leaderboard boards, anti-gaming |
| [12-decision-single-container.md](12-decision-single-container.md) | **Decision:** single Go container backend + thin front Worker; dashboard; badge builder |
| [13-load-and-limits.md](13-load-and-limits.md) | What breaks under a traffic spike and the fixes: D1 via binding not REST, batched Iceberg writes, incremental GitHub refresh, visible job queue |
| [09-ai-timeline-and-attribution.md](09-ai-timeline-and-attribution.md) | AI release milestones on the chart; per-tool (Claude, Copilot, Codex…) commit attribution |

```
 browser ──► Worker (Rust) ──► D1 (users, stats JSON)
               │    ├──► R2 agentstats-cards (rendered SVG/PNG) ◄──┐
               │    └── edge cache                                │ S3 API upload
               ▼
        Durable Object (per user: job state, single-flight)
               │
               ▼
        Cloudflare Container ── Go analyzer + renderer ──┘
               │
               └──► github.com (bare clone, user token)
```

Principles: no TypeScript (Rust + Go only); render once per job, serve files forever; count what a human would call "code they wrote"; show every exclusion; store aggregates only;
every artifact carries "agentstats by deemwar".
