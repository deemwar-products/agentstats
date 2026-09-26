# 02 — Analyzer + renderer (Go, Cloudflare Container)

Port of `prototype/stats.py`. Stateless HTTP service; one job per request; scratch disk wiped after.

## Interface

`POST /analyze` (only reachable from the Durable Object)

```json
{ "token": "<user-to-server token>", "emails": ["a@x.com", "123+login@users.noreply.github.com"],
  "repos": [{"full_name": "o/r", "clone_url": "https://github.com/o/r.git", "fork": false, "private": true}],
  "agent_start": "2025-01" }
```

Response: `application/x-ndjson`: `{"type":"progress","repo":"o/r","done":12,"total":140}` lines, then one
`{"type":"result", ...Stats}` line (schema in 04-data-model.md). `GET /healthz` for the container probe.
Also a CLI mode for local work: `analyzer local --repos '<glob of bare repos>' --emails a,b --agent-start 2025-01`.

## Pipeline

1. **Clone** each repo as a full bare clone (no checkout). Partial clones (`--filter`) don't help: numstat needs the blobs. Auth by `http.extraHeader: Authorization: Bearer <token>` via `-c`, never in the URL and never
   in the git config on disk. Parallelism 6, 120 s timeout per repo, skip repos > 1 GB (recorded as skipped).
2. **Walk**: `git log --all --no-merges --numstat --format=@@%H|%ae|%aI`.
3. **Author filter**: email ∈ the user's verified emails ∪ `<id>+<login>@users.noreply.github.com` ∪ `<login>@users.noreply.github.com`.
4. **Dedupe** by SHA across all repos (forks and mirrors repeat history).
5. **Per file** (numstat line): skip binary (`-`), renames (`=>`), excluded paths, non-code languages, files adding > 5,000.
6. **Per commit**: buffer the surviving file rows; if the commit touched > 300 files or adds > 25,000 lines, drop the
   whole commit into `dropped[]` (sha, repo, month, files, lines). Otherwise add to the aggregates.
7. **Aggregate** into year, month, language × era, repo × era, commits per era.
8. **Render** every card and badge (06-cards-and-badges.md): SVG via Go `text/template`, PNG via `resvg`.
9. **Upload** to R2 over the S3 API (`aws-sdk-go-v2`, endpoint `https://<account>.r2.cloudflarestorage.com`):
   `u/<login>/v<n>/…` first, then `u/<login>/current.json`; prune versions older than n-1.
10. **Wipe** the scratch clone dir, return the result line.

## Exclusion rules (single source: `analyzer/rules.go`, mirrored in the site's methodology page)

- Paths: `node_modules/ vendor/ dist/ build/ target/ .next/ coverage/ bower_components/ out/ __pycache__/ Pods/ .gradle/ venv/ .venv/`,
  any `*aot/` directory, `vendor` anywhere, anything containing `generated`.
- Files: lockfiles (`package-lock.json yarn.lock pnpm-lock.yaml go.sum Gemfile.lock Cargo.lock poetry.lock composer.lock bun.lock*`),
  `*.min.js|css`, `*.map`, `*.snap`, `*.svg`, `*.lock`, `*.csv|tsv|txt|log`, `*.pb.go`, `*_pb2.py`, `*.g.dart`.
- Languages counted: extension map in `rules.go`. JSON, XML, Markdown, YAML, TOML and notebooks are **not** code.

## Regression test

`prototype/expected-muthuishere.json` from the Python prototype. Go output on the same mirrors must match
year/era totals exactly. Add fixture repos for each rule (lockfile, vendored dir, fork import commit, generated dir).

## Limits (v1)

500 repos, 2 GB total packs, 15 min wall clock per job. Anything skipped is reported in `skipped[]` and shown on the page.
