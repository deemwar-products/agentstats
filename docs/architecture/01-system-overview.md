# 01 — System overview

> Superseded in part by [12](12-decision-single-container.md): pages, auth and jobs now run in the Go container; the Worker only routes and serves R2 images.


## Language rule

No TypeScript anywhere. Worker = Rust, analyzer + renderer = Go. Pages are server-rendered HTML + CSS.

## Components

| component | tech | job |
|---|---|---|
| Worker | Rust (workers-rs → wasm) | Site (server-rendered HTML, no JS framework), sign-in, API, serves files from R2 |
| Durable Object `UserJob` | Rust (workers-rs `#[durable_object]`) | One per GitHub user: job state machine, single-flight, progress, container handle |
| Container `analyzer` | Go, Cloudflare Containers | Clone repos, run `git log --numstat`, apply rules, **render cards/badges (SVG + PNG) and upload to R2 via S3 API**, return aggregates |
| D1 `agentstats` | SQLite | Users, settings, latest stats JSON, dropped-commit list |
| R2 `agentstats-cards` | R2 (S3-compatible) | Every rendered badge/card SVG + PNG, `u/<login>/…` |
| GitHub App `agentstats-by-deemwar` | owned by `deemwar-products` | User sign-in + read access to repos |

## Flow A — first visit / sign-in

1. `GET /` landing → "Sign in with GitHub".
2. `/auth/login` → GitHub App user authorization (OAuth web flow, with the App's client id).
3. `/auth/callback` → exchange code for a user-to-server token, read `/user` + `/user/emails` (verified only).
4. Upsert user in D1, set a signed session cookie. The token goes to the user's DO in memory; it is not written to D1.
5. Redirect to `/u/:login` → if no stats, start Flow B.

## Flow B — analysis job

1. `POST /api/analyze` → `UserJob` DO (`idFromName(login)`); if a job runs, return its progress (single-flight).
2. DO lists repos with the user token: owned + collaborator + org repos the App can see, forks marked.
3. DO starts / wakes the container instance and `POST /analyze` with `{token, emails[], repos[], agentStart}`.
4. Analyzer streams NDJSON progress (`{repo, done, total}`), renders all cards + badges, uploads them to R2
   (`u/<login>/v<n>/…`, then flips `u/<login>/current.json`), then sends the final aggregate JSON.
5. DO writes the aggregate + version to D1, purges the cache for the user's image URLs, drops the token.
6. The status page reloads itself (`<meta http-equiv=refresh content=5>`, no JS) until done.

## Flow C — badge / card views (the hot path)

`GET /badge/:login.svg`, `/card/:login/before-after.svg`, `/card/:login/composition.svg`
→ edge cache hit, or stream the object from the R2 binding → cache (`s-maxage=21600`).
No rendering, no container, no GitHub call, no D1 on this path.
GitHub's camo proxy fetches these for READMEs, so they must be fast and anonymous.

## Refresh

Manual "Refresh" button (rate-limited 1/day/user) plus an optional weekly cron through the DO for users who opted in.
A refresh needs a valid token, so cron refresh uses a GitHub App installation token when the user installed the App
on their account; otherwise it's manual only.

## Scale notes

- Analysis cost scales with total history size, not visitors. Cap at 500 repos / 2 GB of packs per job (v1).
- Views are pure edge + D1 reads; container instances scale to zero between jobs.
