//! agentstats Worker (Rust, workers-rs). Server-rendered HTML (askama), GitHub
//! App sign-in, job trigger, and serving pre-rendered cards/badges straight
//! from R2. **It renders no cards** — the Go container does all image rendering
//! and uploads to R2; the Worker only resolves the version pointer and streams
//! bytes with cache headers. See docs/architecture/03-worker-api.md.
//!
//! No secret values ever appear here. Credentials come from Worker secrets
//! (see wrangler.toml header). Placeholders below are OBVIOUSLY fake.

use askama::Template;
use worker::*;

mod durable;
mod models;
mod session;

pub use durable::UserJob;

use models::CurrentPointer;

const FOOTER: &str = "agentstats by deemwar";
const SITE: &str = "https://agentstats.deemwar.com";
const DEEMWAR: &str = "https://deemwar.com";

// The AI release timeline is single-sourced from the analyzer's data file so
// adding a model is a one-line PR in one place.
const MILESTONES_JSON: &str = include_str!("../../analyzer/milestones.json");

#[event(fetch)]
async fn fetch(req: Request, env: Env, _ctx: Context) -> Result<Response> {
    Router::new()
        .get_async("/", |_, _| async { render(LandingTpl { footer: FOOTER, deemwar: DEEMWAR, site: SITE }) })
        .get_async("/methodology", |_, _| async { methodology() })
        .get_async("/auth/login", auth_login)
        .get_async("/auth/callback", auth_callback)
        .post_async("/auth/logout", auth_logout)
        .get_async("/u/:login", profile)
        .get_async("/u/:login/status", status)
        .post_async("/api/analyze", api_analyze)
        .post_async("/api/settings", api_settings)
        .post_async("/api/delete", api_delete)
        .get_async("/badge/:login", |_, ctx| async move {
            let login = strip_ext(&param(&ctx, "login"), ".svg");
            serve_card(&ctx.env, &login, "badge.svg", "image/svg+xml").await
        })
        .get_async("/card/:login/before-after.svg", |_, ctx| async move {
            serve_card(&ctx.env, &param(&ctx, "login"), "before-after.svg", "image/svg+xml").await
        })
        .get_async("/card/:login/before-after.png", |_, ctx| async move {
            serve_card(&ctx.env, &param(&ctx, "login"), "before-after.png", "image/png").await
        })
        .get_async("/card/:login/composition.svg", |_, ctx| async move {
            serve_card(&ctx.env, &param(&ctx, "login"), "composition.svg", "image/svg+xml").await
        })
        .get_async("/card/:login/composition.png", |_, ctx| async move {
            serve_card(&ctx.env, &param(&ctx, "login"), "composition.png", "image/png").await
        })
        .get_async("/og/:login", |_, ctx| async move {
            let login = strip_ext(&param(&ctx, "login"), ".png");
            serve_card(&ctx.env, &login, "og.png", "image/png").await
        })
        .run(req, env)
        .await
}

// ---------------------------------------------------------------------------
// Serving images from R2 (the hot path): resolve version pointer, stream bytes.
// No rendering, no container, no GitHub call, no D1 here.
// ---------------------------------------------------------------------------

async fn serve_card(env: &Env, login: &str, file: &str, content_type: &str) -> Result<Response> {
    let login = login.to_lowercase();
    let bucket = env.bucket("CARDS")?;

    // Resolve u/<login>/current.json -> { "v": n }.
    let ptr_key = format!("u/{login}/current.json");
    let version = match bucket.get(&ptr_key).execute().await? {
        Some(obj) => match obj.body() {
            Some(b) => serde_json::from_slice::<CurrentPointer>(&b.bytes().await?).ok().map(|p| p.v),
            None => None,
        },
        None => None,
    };
    let Some(v) = version else {
        return placeholder(content_type);
    };

    let key = format!("u/{login}/v{v}/{file}");
    match bucket.get(&key).execute().await? {
        Some(obj) => {
            let Some(body) = obj.body() else { return placeholder(content_type) };
            let bytes = body.bytes().await?;
            let mut headers = Headers::new();
            headers.set("Content-Type", content_type)?;
            // GitHub's camo proxy fetches READMEs — cache hard at the edge.
            headers.set("Cache-Control", "public, max-age=3600, s-maxage=21600")?;
            let etag = obj.http_etag();
            if !etag.is_empty() {
                headers.set("ETag", &etag)?;
            }
            Ok(Response::from_bytes(bytes)?.with_headers(headers))
        }
        None => placeholder(content_type),
    }
}

/// A tiny "no stats yet" SVG for missing objects / private profiles, cached 5 min.
fn placeholder(content_type: &str) -> Result<Response> {
    let svg = format!(
        r##"<svg xmlns="http://www.w3.org/2000/svg" width="480" height="120" role="img">
<rect width="480" height="120" rx="10" fill="#0d1117"/>
<text x="24" y="54" fill="#e6edf3" font-family="system-ui,sans-serif" font-size="18">no stats yet</text>
<text x="24" y="84" fill="#8b949e" font-family="system-ui,sans-serif" font-size="13">{SITE}</text>
</svg>"##
    );
    let mut headers = Headers::new();
    let ct = if content_type == "image/png" { "image/svg+xml" } else { content_type };
    headers.set("Content-Type", ct)?;
    headers.set("Cache-Control", "public, max-age=300")?;
    Ok(Response::from_bytes(svg.into_bytes())?.with_headers(headers))
}

// ---------------------------------------------------------------------------
// Auth (GitHub App user-to-server). STUBBED where real credentials plug in.
// ---------------------------------------------------------------------------

async fn auth_login(_req: Request, ctx: RouteContext<()>) -> Result<Response> {
    // TODO(owner): client_id comes from the GITHUB_CLIENT_ID secret, set only
    // after the GitHub App is created (manifest flow — docs 05). Do not invent it.
    let client_id = ctx
        .env
        .secret("GITHUB_CLIENT_ID")
        .map(|s| s.to_string())
        .unwrap_or_else(|_| "YOUR_GITHUB_CLIENT_ID".to_string());

    // Real flow: generate `state` + PKCE `code_challenge`, store in a short cookie,
    // redirect to github.com/login/oauth/authorize. Left as a structural redirect.
    let state = random_token();
    let url = format!(
        "https://github.com/login/oauth/authorize?client_id={client_id}&state={state}&scope="
    );
    let mut headers = Headers::new();
    headers.set("Location", &url)?;
    // TODO: also Set-Cookie the state/PKCE verifier (HttpOnly) for callback validation.
    Ok(Response::empty()?.with_status(302).with_headers(headers))
}

async fn auth_callback(_req: Request, _ctx: RouteContext<()>) -> Result<Response> {
    // TODO(owner/impl): exchange ?code for a user-to-server token using
    // GITHUB_CLIENT_ID + GITHUB_CLIENT_SECRET (secrets), read GET /user and
    // GET /user/emails (verified only), upsert the D1 users row, hand the token
    // to the UserJob DO, then set the signed session cookie and redirect to
    // /u/:login. Stubbed until the GitHub App exists.
    Response::error("auth callback not wired: GitHub App not yet created (owner step, docs 05)", 501)
}

async fn auth_logout(_req: Request, _ctx: RouteContext<()>) -> Result<Response> {
    let mut headers = Headers::new();
    headers.set("Set-Cookie", &session::clear_cookie_header())?;
    headers.set("Location", "/")?;
    Ok(Response::empty()?.with_status(302).with_headers(headers))
}

// ---------------------------------------------------------------------------
// Pages.
// ---------------------------------------------------------------------------

async fn profile(_req: Request, ctx: RouteContext<()>) -> Result<Response> {
    let login = param(&ctx, "login").to_lowercase();
    // TODO(impl): read the users + stats rows from D1 (binding DB) to fill the
    // headline and excluded-commits summary. Cards themselves load from the
    // image routes below, so the page renders even before D1 is populated.
    render(ProfileTpl {
        login: login.clone(),
        site: SITE,
        footer: FOOTER,
        deemwar: DEEMWAR,
        badge_url: format!("{SITE}/badge/{login}.svg"),
        before_after_url: format!("{SITE}/card/{login}/before-after.svg"),
        composition_url: format!("{SITE}/card/{login}/composition.svg"),
    })
}

async fn status(_req: Request, ctx: RouteContext<()>) -> Result<Response> {
    let login = param(&ctx, "login");
    // TODO(impl): fetch UserJob DO /state via env.durable_object("USER_JOB")
    //   .id_from_name(&login) and show the real progress. Meta-refresh drives it.
    render(StatusTpl { login, footer: FOOTER, deemwar: DEEMWAR })
}

fn methodology() -> Result<Response> {
    // Exclusion rules mirrored from analyzer/rules.go (single source of truth in
    // Go; keep this list in sync). Milestones single-sourced from the embedded
    // analyzer/milestones.json (dates UNVERIFIED — see docs 09).
    render(MethodologyTpl {
        footer: FOOTER,
        deemwar: DEEMWAR,
        milestones_json: MILESTONES_JSON,
    })
}

// ---------------------------------------------------------------------------
// API (session-guarded). Wiring stubbed where infra is owner-gated.
// ---------------------------------------------------------------------------

async fn api_analyze(req: Request, ctx: RouteContext<()>) -> Result<Response> {
    let Some(_uid) = current_user(&req, &ctx).await else {
        return Response::error("sign in first", 401);
    };
    // TODO(impl): resolve login for uid from D1, forward to UserJob DO /start with
    // { token(from session-linked DO memory), login, emails, repos, agent_start }.
    // Single-flight is enforced inside the DO.
    Response::error("analyze not wired: container/GitHub App owner-gated (docs 03/05)", 501)
}

async fn api_settings(req: Request, ctx: RouteContext<()>) -> Result<Response> {
    let Some(_uid) = current_user(&req, &ctx).await else {
        return Response::error("sign in first", 401);
    };
    // TODO(impl): update agent_start / is_public / excluded_repos in D1 for uid.
    Response::error("not implemented", 501)
}

async fn api_delete(req: Request, ctx: RouteContext<()>) -> Result<Response> {
    let Some(_uid) = current_user(&req, &ctx).await else {
        return Response::error("sign in first", 401);
    };
    // TODO(impl): delete D1 users/stats rows for uid (ON DELETE CASCADE) and every
    // R2 object under u/<login>/ (list + delete), then clear the session cookie.
    Response::error("not implemented", 501)
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

/// Resolve the signed session cookie to a github_id, or None.
async fn current_user(req: &Request, ctx: &RouteContext<()>) -> Option<i64> {
    let key = ctx.env.secret("SESSION_KEY").ok()?.to_string();
    let cookie = req.headers().get("Cookie").ok().flatten()?;
    let val = session::read_cookie(&cookie)?;
    let now = (Date::now().as_millis() / 1000) as i64;
    session::verify(&val, key.as_bytes(), now)
}

fn param(ctx: &RouteContext<()>, name: &str) -> String {
    ctx.param(name).cloned().unwrap_or_default()
}

fn strip_ext(s: &str, ext: &str) -> String {
    s.strip_suffix(ext).unwrap_or(s).to_string()
}

fn random_token() -> String {
    let mut buf = [0u8; 16];
    // getrandom(js feature) works in the Workers runtime.
    getrandom::getrandom(&mut buf).ok();
    hex::encode(buf)
}

fn render<T: Template>(t: T) -> Result<Response> {
    match t.render() {
        Ok(html) => {
            let mut headers = Headers::new();
            headers.set("Content-Type", "text/html; charset=utf-8")?;
            Ok(Response::from_bytes(html.into_bytes())?.with_headers(headers))
        }
        Err(e) => Response::error(format!("template error: {e}"), 500),
    }
}

// ---------------------------------------------------------------------------
// Templates.
// ---------------------------------------------------------------------------

#[derive(Template)]
#[template(path = "landing.html")]
struct LandingTpl {
    footer: &'static str,
    deemwar: &'static str,
    site: &'static str,
}

#[derive(Template)]
#[template(path = "methodology.html")]
struct MethodologyTpl {
    footer: &'static str,
    deemwar: &'static str,
    milestones_json: &'static str,
}

#[derive(Template)]
#[template(path = "profile.html")]
struct ProfileTpl {
    login: String,
    site: &'static str,
    footer: &'static str,
    deemwar: &'static str,
    badge_url: String,
    before_after_url: String,
    composition_url: String,
}

#[derive(Template)]
#[template(path = "status.html")]
struct StatusTpl {
    login: String,
    footer: &'static str,
    deemwar: &'static str,
}
