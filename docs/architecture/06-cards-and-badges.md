# 06 — Cards and badges

All rendered in the **Go container** at the end of a job: SVG from Go `text/template`, PNG by piping the SVG
through `resvg` (a static binary in the container image) with the fonts bundled in the image. Uploaded to R2.
The Worker never renders. Reference designs: `docs/reference/card-1-before-after.png`, `card-2-composition-monthly.png`.

## Card 1 — Before / After (`before-after.svg|png`, 800 px wide)

- Kicker: `968K LINES · 33,501 COMMITS · 2004–2026` (totals, first year – current year).
- Headline, picked by rule from the numbers:
  - after ≥ before → "I wrote more code in the last N months than in the previous M years."
  - after ≥ 0.5 × before → "I wrote half as much code in the last N months as in the previous M years."
  - otherwise → "N months of agents: X lines, Y× my old pace."
- Two panels: **BEFORE AGENTS** (light) / **AFTER AGENTS** (dark): years span, lines (big), top language + %,
  commits, pace (per year before; "this year so far" after), languages ("4 cover 94%" / "12 in daily use").
- Callout: "The pace changed, not the hours. …" with the peak month vs the best pre-agent year.
- Footer (always): "excluded: 3.2M lines of imports & generated code · agentstats by deemwar".

## Card 2 — Composition (`composition.svg|png`)

- Kicker `WHAT THE CODE IS MADE OF`; headline "<top before-lang> was X% of my work. Now it is Y%."
  (or "Now <top after-lang> is Z%." when the old one fell out of the top 6).
- Two 100 % stacked bars (before, after): top 6 languages + Other, legend with %, era label from a small
  lookup (e.g. Ruby+JS+Views → "a Ruby web stack"; Go/Rust/Shell → "systems and tooling").
- **Lines per month since agent start**: bars, peak month in accent colour, caption "The red bar is July 2026: 567K lines in one month."
- Three pairs: lines per commit, repos touched, languages in daily use (before → after).

## Badge (`badge.svg`)

Shields-style, two segments: `agent era | 1.25M lines · 1.8×` (the multiplier is after-pace ÷ before-pace).
Links (in the embed snippet) to `https://agentstats.deemwar.com/u/<login>`.

Embed snippets shown on the profile page:
```md
[![agentstats](https://agentstats.deemwar.com/badge/<login>.svg)](https://agentstats.deemwar.com/u/<login>)
![before and after agents](https://agentstats.deemwar.com/card/<login>/before-after.svg)
```

## Style

One palette, light + dark variants (`?theme=dark` → separate R2 objects), system font stack in SVG and bundled
Inter for PNG. Numbers formatted as 647K / 1.25M. Every artifact carries "agentstats by deemwar".
