# 05 — GitHub App and auth

## The App

- Name: `agentstats by deemwar` · owner: org `deemwar-products` · homepage `https://agentstats.deemwar.com`
- Callback: `https://agentstats.deemwar.com/auth/callback` · "Request user authorization during installation": on
- Permissions — Repository: **Contents: read**, **Metadata: read**. Account: **Email addresses: read**. Nothing else.
- Install scope: the install screen defaults to **All repositories** (public + private, personal and any org the
  user installs it on); the user can narrow it to selected repos.
- Webhooks: off in v1.
- Created by the owner through the **manifest flow** (a pre-filled form; one click). The app id, client id,
  client secret and private key go straight into Worker secrets (`wrangler secret put`) — never into the repo or chat.

## Why a GitHub App and not an OAuth App

Fine-grained: the user picks which repos (all or selected) at install time, and the consent screen says
"read-only contents". Installation tokens also allow the optional weekly refresh without a stored user token.

## Sign-in flow

1. `/auth/login` → `https://github.com/login/oauth/authorize?client_id=…&state=…&code_challenge=…`.
2. `/auth/callback` → `POST https://github.com/login/oauth/access_token` → user-to-server token (8 h expiry).
3. `GET /user`, `GET /user/emails` (keep `verified: true` only) → D1 upsert → session cookie
   (`HttpOnly; Secure; SameSite=Lax`, HMAC-signed `github_id|exp`, 30 days).
4. Repo list for a job: `GET /user/installations/{id}/repositories` (what the user granted) plus public repos
   they own or contributed to via `GET /user/repos?affiliation=owner,collaborator,organization_member`.

## Token rules

- The user token exists only in the `UserJob` DO memory and in the container's process env during one job.
- The analyzer passes it to git via `-c http.extraHeader="Authorization: Bearer …"`. Never in a URL, a remote,
  a git config file on disk, or a log line. The analyzer's logger redacts `gh[opsu]_…` and `ghu_…` patterns.
- After the job the token is dropped. A new job needs a fresh sign-in or an installation token.
