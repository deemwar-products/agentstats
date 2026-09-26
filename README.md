# agentstats

An honest **"before agents vs after agents"** card for the code you actually wrote — plus a README
badge. Sign in with GitHub, get a shareable card of your real authored lines across your coding
history, with the excluded bulk-imports and generated code shown on the card itself.

The edge is **honest counting**: naive line counts are inflated by vendored code, lock files,
generated files and bulk imports. agentstats excludes them and *shows what it excluded*, so the
number is defensible.

> agentstats is a deemwar product — see [deemwar.com](https://deemwar.com).

## Status

Pre-launch. See [`LAUNCH.md`](./LAUNCH.md) for the go-live checklist and [`BRIEF.md`](./BRIEF.md)
for the full spec, counting rules and architecture.

## Layout

- `analyzer/` — Go analyzer: bare-clone repos, run `git log --numstat`, apply the counting rules,
  emit per-era aggregates. Runs in a Cloudflare Container.
- `prototype/` — the reference Python counter (`stats.py`) and its regression fixture.
- `docs/` — reference card designs and design notes.
