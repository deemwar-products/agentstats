# 09 — AI timeline and per-tool attribution

Two features, both from git history alone:

1. **AI timeline overlay** — mark when each AI coding tool and model shipped on the user's monthly chart, so the
   card shows *which release* bent their curve ("your pace tripled the month after Claude Opus 4.5").
2. **Per-tool attribution** — how many of the user's commits and lines carry an agent's signature (ChatGPT/Codex,
   GitHub Copilot, Claude, Cursor, Devin, …).

## 1. Milestones (`analyzer/milestones.json`, data not code)

```json
[
  {"date": "2021-06-29", "tool": "GitHub Copilot", "event": "technical preview", "kind": "assistant"},
  {"date": "2022-06-21", "tool": "GitHub Copilot", "event": "generally available", "kind": "assistant"},
  {"date": "2022-11-30", "tool": "ChatGPT", "event": "launch", "kind": "chat"},
  {"date": "2023-03-14", "tool": "GPT-4", "event": "release", "kind": "model"},
  {"date": "2023-03-14", "tool": "Claude", "event": "first release", "kind": "model"},
  {"date": "2024-03-04", "tool": "Claude 3 Opus", "event": "release", "kind": "model"},
  {"date": "2024-06-20", "tool": "Claude 3.5 Sonnet", "event": "release", "kind": "model"},
  {"date": "2025-02-24", "tool": "Claude Code", "event": "research preview (with Claude 3.7 Sonnet)", "kind": "agent"},
  {"date": "2025-04-16", "tool": "OpenAI Codex CLI", "event": "release", "kind": "agent"},
  {"date": "2025-05-16", "tool": "OpenAI Codex", "event": "cloud agent", "kind": "agent"},
  {"date": "2025-05-19", "tool": "GitHub Copilot coding agent", "event": "preview", "kind": "agent"},
  {"date": "2025-05-22", "tool": "Claude Opus 4 / Claude Code GA", "event": "release", "kind": "agent"},
  {"date": "2025-08-07", "tool": "GPT-5", "event": "release", "kind": "model"},
  {"date": "2025-09-29", "tool": "Claude Sonnet 4.5", "event": "release", "kind": "model"},
  {"date": "2025-11-24", "tool": "Claude Opus 4.5", "event": "release", "kind": "model"}
]
```

**Verify every date against the vendor's own announcement before launch** and add a `source` URL per entry;
the list above was drafted from memory, and 2026 releases still need adding. The methodology page renders this
file, so a wrong date is public. Keep it data, so adding a model is a one-line PR anyone can send (public repo).

Uses:
- Monthly chart (card 2): thin vertical ticks labelled with the tool for `kind: agent` and major models.
- "Agent era start" default: the user's first month with an agent-signed commit (section 2); otherwise
  the month of the first `kind: agent` milestone; else `2025-01`. The user can override it.
- Callout generator: the milestone just before the user's biggest month-over-month jump.

## 2. Per-tool attribution (commit signatures)

Agents leave fingerprints the analyzer already reads (`git log` with `%an %ae %(trailers)`):

| tool | signal |
|---|---|
| Claude Code | trailer `Co-Authored-By: Claude …<noreply@anthropic.com>` (model name in the trailer, e.g. "Claude Opus 4.5"); `🤖 Generated with Claude Code` in the body; author `claude[bot]` |
| GitHub Copilot | author `copilot-swe-agent[bot]` / `Copilot`; trailer `Co-authored-by: Copilot` |
| OpenAI Codex | author/trailer `codex` / `chatgpt-codex-connector[bot]` |
| Cursor | trailer `Co-authored-by: Cursor Agent` / author `cursor[bot]` |
| Devin | author `devin-ai-integration[bot]` |
| Aider | trailer `Co-authored-by: aider` / message prefix `aider:` |

Keep this table in `analyzer/agents.json` (pattern → tool), same as milestones.

Output added to `Stats`:
```json
"agents": {
  "signed_commits": 812, "signed_lines": 402113, "share_of_after_era": 0.32,
  "by_tool": {"Claude Code": {"commits": 700, "lines": 380000, "models": {"Claude Opus 4.5": 410}},
              "GitHub Copilot": {"commits": 89, "lines": 15000}},
  "first_signed_month": "2025-03"
}
```

Bot-authored commits on the user's own repos count toward the user when the App can see the repo and the bot
commit is on it (the user drove the agent). They're shown separately on the card, never silently merged.

## What we can't see

Time spent in ChatGPT, Copilot chat, or any agent session doesn't reach git, and a GitHub App can't read it.
Unsigned agent code looks like human code. So the card says "**at least** N% agent-signed", never "N% AI".
A later opt-in could import local usage logs (ccusage-style) for hours and tokens. Not in v1.
