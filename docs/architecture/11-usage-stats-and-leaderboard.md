# 11 — Usage stats and leaderboard (web app)

Owner direction (2026-09-25): the web app shows how many people have used it, plus a leaderboard.

## Live usage counter (landing page hero + footer)

"**12,408 developers** analyzed · **1.9B honest lines** counted · **3.1B lines of imports and generated code excluded**"

- Numbers come from D1 (`COUNT(*)` over users with stats; `SUM` from stats) and are cached 5 min in KV or the edge cache.
- The "excluded" number is the product's pitch in one line, so show it.
- Also on `/state`: signups per day and analyses per day (from lake `jobs`).

## Leaderboard `/leaderboard`

**Opt-in only.** Settings has a "Show me on the leaderboard" toggle, off by default, and only public profiles can
turn it on. Private-repo lines count toward a user's total but never reveal repo names.

Boards (tabs):
| board | ranks by |
|---|---|
| Agent-era output | lines written since agent start (honest count) |
| Biggest shift | after-pace ÷ before-pace (min 2 years of before-history and 5K before lines, to stop new accounts gaming it) |
| Peak month | best single month |
| By tool | agent-signed lines per tool (Claude Code, Copilot, Codex, Cursor…) |
| By language | agent-era lines per language (Go, Rust, Python, …) |

Filters: time window (all / this year / last 90 days), language.
Each row: avatar, login → `/u/:login`, the number, a sparkline, and a "copy my rank badge" link (a rank badge
is another R2-rendered SVG: `#42 on agentstats · Biggest shift`).

## Anti-gaming

Honest-count rules already strip imports, generated code and bulk commits. On top of that:
- Only commits the GitHub App can verify on GitHub count; no uploaded numbers.
- Flag outliers (> 50K lines/day sustained, or a single repo > 80 % of the total) for review; hide them until reviewed.
- Recompute on refresh only (once a day), so ranks can't be spammed.

## Data

D1 table `leaderboard` (github_id, board, window, value, rank, updated_at), rebuilt by the daily cron
from `stats` (and the lake for tool/language boards); rendered pages cached 10 min.
