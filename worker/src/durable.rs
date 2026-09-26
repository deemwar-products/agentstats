//! `UserJob` Durable Object — one per GitHub user. Owns the analysis job state
//! machine, enforces single-flight, holds the user token in MEMORY ONLY (never
//! persisted to storage), drives the analyzer container, and writes the
//! aggregate result to D1. See docs/architecture/03-worker-api.md.

use serde::{Deserialize, Serialize};
use worker::*;

/// Job state. Only `progress`/version is persisted to DO storage; the token is
/// never stored — it lives in `self.token` for the life of the instance.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(tag = "state", rename_all = "snake_case")]
pub enum JobState {
    Idle,
    Listing,
    Analyzing { done: u32, total: u32 },
    Failed { reason: String },
    Done { v: u32 },
}

impl Default for JobState {
    fn default() -> Self {
        JobState::Idle
    }
}

#[durable_object]
pub struct UserJob {
    state: State,
    env: Env,
    /// User-to-server token, in memory only, dropped when the job ends. NEVER
    /// written to `self.state.storage()`.
    token: Option<String>,
}

#[durable_object]
impl DurableObject for UserJob {
    fn new(state: State, env: Env) -> Self {
        Self { state, env, token: None }
    }

    async fn fetch(&mut self, mut req: Request) -> Result<Response> {
        let path = req.path();
        match (req.method(), path.as_str()) {
            // Start or join a job (single-flight). Body: { token, login, emails, repos, agent_start }.
            (Method::Post, "/start") => {
                let cur = self.load_state().await;
                if is_running(&cur) {
                    // Single-flight: a job is already in progress — return its state.
                    return Response::from_json(&cur);
                }
                let body: StartJob = req.json().await?;
                // Token stays in memory only.
                self.token = Some(body.token.clone());
                self.save_state(&JobState::Listing).await?;

                // TODO(container): call the Analyzer container via the Containers
                // binding (`self.env.container("ANALYZER")?`), POST /analyze with
                // { token, emails, repos, agent_start }, and consume the NDJSON
                // progress stream, calling `self.save_state(Analyzing{done,total})`
                // per line. On the final {"type":"result", ...Stats} line, write
                // D1 (users.stats + jobs) and flip to Done{v}. The container image
                // renders every card/badge to R2 before emitting the result, so the
                // Worker never renders. On error → Failed{reason}. Always drop the
                // token (`self.token = None`) in every terminal branch.
                //
                // Left as a state-machine stub until the container + Containers
                // binding are wired at deploy time (owner-gated infra).

                Response::from_json(&JobState::Listing)
            }
            // Poll current job state (drives the /u/:login/status meta-refresh page).
            (Method::Get, "/state") => {
                let cur = self.load_state().await;
                Response::from_json(&cur)
            }
            _ => Response::error("not found", 404),
        }
    }
}

impl UserJob {
    async fn load_state(&self) -> JobState {
        self.state
            .storage()
            .get::<JobState>("state")
            .await
            .unwrap_or_default()
    }

    async fn save_state(&mut self, s: &JobState) -> Result<()> {
        self.state.storage().put("state", s).await
    }
}

fn is_running(s: &JobState) -> bool {
    matches!(s, JobState::Listing | JobState::Analyzing { .. })
}

#[derive(Debug, Deserialize)]
struct StartJob {
    token: String,
    #[allow(dead_code)]
    login: String,
    #[allow(dead_code)]
    emails: Vec<String>,
    #[allow(dead_code)]
    repos: Vec<serde_json::Value>,
    #[allow(dead_code)]
    agent_start: String,
}
