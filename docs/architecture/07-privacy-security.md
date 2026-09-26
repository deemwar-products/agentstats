# 07 — Privacy and security

## What we read, keep, discard

| data | read | kept | where |
|---|---|---|---|
| Repo contents | cloned for one job (numstat needs blobs) | **no**: scratch disk wiped at job end | container |
| Commit metadata (sha, author email, date, per-file line counts) | yes | only as aggregates | D1 `stats_json` |
| File paths | yes (for rules and language) | no, except repo names in `excluded.top` | — |
| Verified emails | yes | **no**: passed to the job only | — |
| User token | yes | **no**: DO memory + job env only | — |
| Profile (login, name, avatar) | yes | yes | D1 `users` |
| Rendered cards | — | yes, public unless the profile is private | R2 |

Private repos contribute to totals only. Their names never appear on public cards or pages; `excluded.top`
lists a private repo as "a private repo".

## Controls for the user

Private profile (the image URLs return the placeholder), exclude repos, change agent-era start, delete all data
(D1 rows + R2 prefix, immediately), revoke the App on GitHub.

## Abuse and cost limits

- One job per user per 24 h; 500 repos / 2 GB / 15 min per job; global concurrency cap on container instances.
- Image routes are anonymous and cache-heavy; rate-limit `/api/*` per IP and per session with Workers rate limiting.
- The container accepts requests only from the DO (Containers binding, not a public route).

## Secrets

Worker secrets: `GITHUB_APP_ID`, `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET`, `GITHUB_APP_PRIVATE_KEY`,
`SESSION_KEY`. Container secrets: `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY` (a token scoped to the one bucket).
Set with `wrangler secret put` from `sec`. Never in the repo (public), never in logs, never in chat.
