# 03 — Worker (Rust, workers-rs)

> Superseded by [12](12-decision-single-container.md): these routes are served by the Go container; only `/badge/*`, `/card/*` and `/og/*` stay on the front Worker (R2). The Durable Object is replaced by a Go job queue.


One Worker crate `worker/`, compiled to wasm with `worker-build`. No TypeScript, no JS framework; HTML is
rendered in Rust (askama templates) with plain CSS.

## Routes

| route | auth | does |
|---|---|---|
| `GET /` | — | Landing: sample cards, "Sign in with GitHub", methodology link |
| `GET /methodology` | — | The exclusion rules, generated from the same list as `analyzer/rules.go` |
| `GET /auth/login` | — | Redirect to GitHub App user authorization (state cookie, PKCE) |
| `GET /auth/callback` | — | Code → user token; read `/user` + verified `/user/emails`; upsert user; session cookie; hand token to DO |
| `POST /auth/logout` | session | Clear session |
| `GET /u/:login` | — | Public profile: both cards, badge embed snippets, excluded-commits list, "Refresh" if it's you |
| `GET /u/:login/status` | — | Job progress page; `<meta refresh>` until done |
| `POST /api/analyze` | session | Start / join the job in `UserJob` DO (1 per user per 24 h) |
| `POST /api/settings` | session | Agent-era start month, public/private profile, excluded repos |
| `POST /api/delete` | session | Delete D1 rows + R2 prefix `u/<login>/` |
| `GET /badge/:login.svg` | — | R2 `u/<login>/current/badge.svg` |
| `GET /card/:login/before-after.(svg\|png)` | — | R2 object, same pattern |
| `GET /card/:login/composition.(svg\|png)` | — | R2 object |
| `GET /og/:login.png` | — | 1200×630 social preview (R2) for `<meta property="og:image">` |

Missing object → a placeholder SVG ("no stats yet — agentstats.deemwar.com"), cached 5 min.

## Serving images

`env.bucket("CARDS")?.get(key)` → stream body; headers `Content-Type`, `Cache-Control: public, max-age=3600,
s-maxage=21600`, `ETag` from R2. GitHub's camo proxy respects these. `current/` is a small pointer: the analyzer
writes `u/<login>/v<n>/…` then `u/<login>/current.json` → `{ "v": n }`; the Worker resolves the version so an
in-progress upload is never half-visible, then purges the edge cache for that user's URLs.

## Durable Object `UserJob` (Rust `#[durable_object]`)

State: `idle | listing | analyzing{done,total} | failed{reason} | done{v}`; storage holds progress only.
The user token lives in DO memory for the job's life and is never persisted. Single-flight: a second
`/api/analyze` while running returns the current state. Talks to the container through the Containers binding
(`env.container("ANALYZER")`), consumes the NDJSON stream, writes D1 at the end.

## Crates

`worker`, `askama`, `serde`/`serde_json`, `hmac`+`sha2` (session cookie), `getrandom` (wasm_js).
