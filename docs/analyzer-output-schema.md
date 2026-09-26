# Analyzer output JSON — the contract both cards consume

The Go analyzer (`analyzer/`, ported from `prototype/stats.py`) emits one JSON
object of **aggregates only** — no code, no tokens, no file contents. It is what
the container uploads (as `stats.json`) and what D1 stores in `stats.stats_json`.
Both card renderers and the badge read only this object. See also
`docs/architecture/04-data-model.md`.

Produced locally by:

```sh
analyzer local --repos '~/data-exporter-local/raw/github-code/*/*/*.git' \
  --emails muthuishere@gmail.com,oss@deemwar.com --agent-start 2025-01 --login muthuishere
```

## Top-level shape

```jsonc
{
  "login": "muthuishere",
  "agent_start": "2025-01",          // YYYY-MM, the before/after boundary (a month, not just a year)
  "first_year": "2013",
  "totals":  { "lines": 1638308, "commits": 5150, "repos": 326 },

  "eras": {
    "before": { … Era … },           // months < agent_start
    "after":  { … Era … }            // months >= agent_start
  },

  "years":  { "2013": { "lines": 10441, "commits": 22 }, … },   // per calendar year
  "months": { "2025-01": 1234, "2026-07": 567485, … },          // YYYY-MM -> counted lines

  "peak_month": { "month": "2026-07", "lines": 567485, "beats_best_year": "2024" },

  "excluded": {                        // bulk-import commits dropped by the rules
    "commits": 25, "lines": 4360000,
    "top": [ { "repo": "a private repo", "sha": "ecb85df8", "files": 16239, "lines": 3259905, "reason": "bulk-import" } ]
  },

  "agents": { … AgentStats … },        // per-tool commit-signature attribution (docs 09); omitted if not requested

  "skipped_repos": [ { "repo": "o/huge", "reason": "size>1GB" } ],

  // --- reproduction fields (same shape prototype/stats.py emits; the regression test asserts these) ---
  "commits_by_year": { "2013": 22, …, "2026": 3529 },   // NOTE: includes bulk-import-dropped commits
  "lines_by_year":   { "2013": 10441, …, "2026": 1128412 },  // excludes dropped commits
  "lang_by_year":    { "2026": { "Go": 548602, … }, … },
  "repos_github": 326,
  "repos_gitlab": 0,
  "dropped": [ { "month": "2026-07", "repo": "…", "sha": "ecb85df8", "files": 16239, "lines": 3259905 }, … ]
}
```

## `Era`

```jsonc
{
  "from": "2013", "to": "2024",        // year span of the era
  "lines": 386569,                     // counted code lines (dropped commits excluded)
  "commits": 1355,                     // deduped authored commits in the era (INCLUDES dropped — matches commits_by_year)
  "repos_touched": 142,                // distinct repos with a surviving commit in the era
  "lines_per_commit": 285,             // round(lines / commits)
  "langs": { "JavaScript": 152000, … },// counted lines per language in the era
  "daily_langs": 12,                   // languages with >= 2% of the era's lines ("in daily use")
  "pace_per_year": 32214,              // before era only: lines / span-years
  "pace_this_year": 1128412            // after era only: counted lines in the latest year
}
```

Card 1 (before/after) reads `totals`, `first_year`, both eras, `peak_month`,
`years` (best pre-agent year), and `excluded.lines` (the footer). Card 2
(composition) reads both eras' `langs`, `months` (the per-month bar chart, peak
highlighted), and the three era pairs `lines_per_commit` / `repos_touched` /
`daily_langs`.

## `AgentStats` (the `agents` block)

Attribution from commit signatures alone — bot authors and Co-Authored-By /
body trailers (see `docs/architecture/09-…`). Unsigned agent code is
indistinguishable from human code, so this is a floor ("at least N% agent-signed").

```jsonc
{
  "signed_commits": 812,
  "signed_lines": 402113,              // counted (non-dropped) lines of signed commits
  "share_of_after_era": 0.32,          // signed after-era lines / after-era counted lines
  "by_tool": {
    "Claude Code":    { "commits": 700, "lines": 380000, "models": { "Claude Opus 4.5": 410 } },
    "GitHub Copilot": { "commits": 89,  "lines": 15000 }
  },
  "first_signed_month": "2025-03"
}
```

The signature patterns live in `analyzer/agents.json` (pattern → tool) and the
release timeline in `analyzer/milestones.json` — both DATA, so adding a tool or a
release is a one-line PR. **Milestone dates are UNVERIFIED**: each needs a
vendor-announcement `source` URL before launch (an owner/launch step).

## Counting rules (single source: `analyzer/rules.go`)

Added lines only (`git log --all --no-merges --numstat`), deduped by SHA across
all repos. Excluded: the `SKIP` path/lock/generated/vendored/minified regex;
non-code languages (JSON, XML, Markdown, YAML, TOML, notebooks); any single file
adding > 5,000 lines; and any whole commit touching > 300 files or adding
> 25,000 counted lines (recorded in `dropped[]`). These reproduce
`prototype/stats.py` exactly — asserted by `analyzer/analyze_test.go` against
`analyzer/testdata/expected-github-only.json`.

> Privacy: private repos contribute to totals only; their names never appear on
> public cards — `excluded.top` lists a private repo as "a private repo".
