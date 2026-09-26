# agentstats — go-live checklist

This repo is **private during the build** and flips to **public** only at launch. The flip to
public and the domain going live are the two owner-gated forks — nothing here goes public without
the owner's explicit go-ahead.

## Owner touchpoints (escalate with a default, do not self-authorize)

- [ ] **Create the GitHub App under `deemwar-products`** — manifest flow, one owner click. Reads all
      repos (public + private): Contents:read + Metadata:read, user-to-server auth. Send the owner the
      manifest link when the code is ready.
- [ ] **Cloudflare resources on Admin@deemwar.com's account** — Worker `agentstats`, D1 `agentstats`,
      R2 bucket `agentstats-cards` + an R2 API token scoped to it, Durable Object `UserJob`, Container
      `analyzer`. **Containers need the Workers Paid plan — confirm it's on the account first.**
- [ ] **Data lake (see doc 10):** enable **R2 Data Catalog** on the account, create bucket
      `agentstats-lake` + a scoped R2 token, and add a cron trigger for the daily rollup.
- [ ] **Flip repo to public + point `agentstats.deemwar.com` at the Worker + first deploy.** This is
      the go-live. Then run the owner's own profile end-to-end and compare to the fixture.

## Must be done BEFORE the repo goes public

- [ ] **Recompute the regression fixture GitHub-only.** `prototype/expected-muthuishere.json` was
      computed over GitHub + GitLab mirrors; the product is GitHub-only. Regenerate the GitHub-only
      subset as the real fixture.
- [ ] **Sanitize the fixture — no private repo names.** The current `dropped` list embeds the
      owner's private repo names (e.g. internal product repos). A public regression fixture must not
      reveal private repo names — redact or generically label them; the counts are what the fixture
      tests.
- [ ] **No Claude / Anthropic attribution anywhere** — no footer, no badge, no `Co-Authored-By` in
      commits. Card/badge footer reads "agentstats by deemwar".
- [ ] **No promotion buried in the code** beyond the intended deemwar product branding.
- [ ] **Demo/placeholder tokens must be obviously fake** — never a realistic-looking key.
- [ ] **Verify every AI-milestone date in `analyzer/milestones.json` against the vendor's own
      announcement and add a `source` URL per entry.** They were drafted from memory; the methodology
      page renders this file, so a wrong date ships publicly. Add 2026 releases.
- [ ] **Load test (doc 13):** 1,000 concurrent badge GETs (all should be edge hits), 200 concurrent
      dashboard loads, 50 queued jobs against test accounts. Record p95 + container instance count; set
      `max_instances` (the cost ceiling) from the result and price the monthly cost at that ceiling.
- [ ] Anonymous fetch of the live card/badge must render correctly (verify logged-out, not just 200).
