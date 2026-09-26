# agentstats — brief (owner-approved 2026-09-25)

**What:** a public site where any developer signs in with GitHub and gets an honest "before agents vs after agents"
card of the code they actually wrote, plus a README badge. Built as a deemwar product so every badge/card
advertises deemwar.

- Domain: `agentstats.deemwar.com` (zone `deemwar.com` is on the Cloudflare account)
- Repo: `deemwar-products/agentstats` — **PUBLIC**
- Local: `~/muthu/deemwarworkspace/products/agentstats`
- Owner: the CEO builds and ships it. Escalate only money/legal/irreversible.

## Target output (the card that inspired it)

"968K lines · 33,501 commits · 2004–2026 — I wrote half as much code in the last 20 months as in the previous 21 years."
Two panels: BEFORE AGENTS (years, lines, top language %, commits, pace/yr, languages) vs AFTER AGENTS
(same + pace this year), and a callout line ("August 2026 shipped 151K — more than any full year").

Reference images: `docs/reference/card-1-before-after.png` and `docs/reference/card-2-composition-monthly.png`.

## Card 2 — "What the code is made of" (owner: also needed)

- Headline generated from the biggest language shift, e.g. "Ruby was 55% of my work. Now it is 3%."
  (owner's own would be "JavaScript was 33% of my work. Now Go is 46%.")
- Two stacked 100% bars — BEFORE years and AFTER years — top ~6 languages + Other, with a percentage legend
  and an auto label for the era ("a Ruby web stack" / "systems, desktop and tooling").
- **Lines per month since agent-era start**: bar chart, peak month highlighted with a caption
  ("The red bar is August 2026: 151K lines in one month").
- Three before → after stat pairs: **lines per commit**, **repos touched**, **languages in daily use**.
- Needs from the analyzer: per-era language totals, per-month lines, distinct repos per era, lines/commit per era,
  count of languages above a daily-use threshold per era.

## Why it wins (competitor check done 2026-09-25)

Nobody does this. Adjacent products and their gap:
- cc-stats-badge / claude-stats / cc-proficiency / ccusage — agent *usage* (hours, tokens) from local logs, self-run cron; no code, no before/after.
- github-readme-stats — no LOC card at all (issue #1526 open for years).
- LineUp / tokei / LOC-badge Actions — one repo snapshot via cloc; no author, no timeline.
- git-ai / GitClear — team line attribution, needs setup in advance; no personal card.
- hgn/statistical-investigation-agentocene — research warning that naive before/after SLOC is misleading.

**Edge = honest counting, shown on the card.** Owner's own raw 2026 total was 4.9M lines; after filters it is 1.13M
(a VS Code fork import alone was 3.26M). The card must state what was excluded ("3.2M lines of imports and
generated code excluded"). That is the trust + virality hook.

## Counting rules (proven in `prototype/stats.py`)

- Author = the user's verified GitHub emails (+ noreply). Dedupe commits by SHA across all repos/forks. `--all --no-merges`.
- Lines = **added** lines from `git log --numstat`, code languages only (by extension).
- Exclude paths: lock files, `node_modules/ vendor/ dist/ build/ target/ .next/ coverage/ Pods/ venv/`, `*.min.*`,
  `*.map`, `*.svg`, csv/txt/log, `*_pb2.py`, `*.pb.go`, `*.g.dart`, anything `generated`, `*aot/` dirs, `vendor` anywhere.
- Exclude non-code: JSON, XML, Markdown, YAML, TOML, notebooks.
- Per-file: skip a single file adding >5,000 lines.
- **Per-commit bulk-import rule:** drop a commit touching >300 files or adding >25,000 lines; list every dropped commit.
- Agent era default start = 2025-01 (user-adjustable).
- Regression fixture: `prototype/expected-muthuishere.json` — 326 GitHub mirrors (+33 GitLab) gave
  BEFORE 2013–2024 = 696,135 lines / 1,814 commits; AFTER 2025–2026 = 1,251,739 / 3,796; 32 dropped commits.
  (Product is GitHub-only; the GitHub-only subset is the real fixture — recompute it.)

## Architecture (proposed)

**Owner rule: no TypeScript anywhere.** Rust + Go only; pages are server-rendered HTML with no JS framework.
Full design in `docs/architecture/`.

- **Worker** (Rust, workers-rs): landing, GitHub App user sign-in, `/u/:login` page, job trigger, and serving
  badge/card files **straight from R2**. It renders nothing.
- **Cloudflare Container** (Go analyzer in `analyzer/`): bare-clone the user's repos with the user token
  (no checkout), run numstat, apply the rules, **render every card and badge (SVG from Go templates, PNG via
  resvg) and upload them to R2 over the S3 API**, return aggregates. Durable Object per user drives it.
- **R2 bucket `agentstats-cards`** (S3-compatible): the rendered files, `u/<login>/…`. Views are R2 reads + edge cache.
- **D1**: users + stats JSON (aggregates only). Never store code or tokens beyond the job.
- **GitHub App** owned by `deemwar-products`: Contents read + Metadata read, user-to-server auth.
  Creating it needs one owner click (manifest flow) — ask the owner at that step.
- Badge/card footer: "agentstats by deemwar" linking to deemwar.com.

## Privacy line (put on the site)

We read commit stats, not your code. Private repos contribute only aggregate numbers. Tokens are used for
the job and discarded. Delete-my-data button.
