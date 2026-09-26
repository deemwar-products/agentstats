//! Serde mirror of the analyzer's `Stats` output contract (aggregates only —
//! no code, no tokens). Kept in lockstep with docs/architecture/04-data-model.md
//! and docs/analyzer-output-schema.md. The Worker only *reads* this JSON out of
//! D1 / R2 to fill page templates; it never recomputes it.

use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Stats {
    pub login: String,
    pub agent_start: String,
    pub first_year: String,
    pub totals: Totals,
    pub eras: Eras,
    #[serde(default)]
    pub years: BTreeMap<String, YearStat>,
    #[serde(default)]
    pub months: BTreeMap<String, i64>,
    pub peak_month: PeakMonth,
    pub excluded: Excluded,
    #[serde(default)]
    pub agents: Option<AgentStats>,
    #[serde(default)]
    pub skipped_repos: Vec<SkippedRepo>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Totals {
    pub lines: i64,
    pub commits: i64,
    pub repos: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Eras {
    pub before: Era,
    pub after: Era,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Era {
    pub from: String,
    pub to: String,
    pub lines: i64,
    pub commits: i64,
    pub repos_touched: i64,
    pub lines_per_commit: i64,
    #[serde(default)]
    pub langs: BTreeMap<String, i64>,
    pub daily_langs: i64,
    #[serde(default)]
    pub pace_per_year: i64,
    #[serde(default)]
    pub pace_this_year: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct YearStat {
    pub lines: i64,
    pub commits: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PeakMonth {
    pub month: String,
    pub lines: i64,
    pub beats_best_year: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Excluded {
    pub commits: i64,
    pub lines: i64,
    #[serde(default)]
    pub top: Vec<DroppedSummary>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DroppedSummary {
    /// A private repo is reported here as "a private repo" — never its real name.
    pub repo: String,
    pub sha: String,
    pub files: i64,
    pub lines: i64,
    pub reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentStats {
    pub signed_commits: i64,
    pub signed_lines: i64,
    pub share_of_after_era: f64,
    #[serde(default)]
    pub by_tool: BTreeMap<String, ToolStat>,
    pub first_signed_month: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ToolStat {
    pub commits: i64,
    pub lines: i64,
    #[serde(default)]
    pub models: BTreeMap<String, i64>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SkippedRepo {
    pub repo: String,
    pub reason: String,
}

/// A D1 `users` row.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UserRow {
    pub github_id: i64,
    pub login: String,
    pub name: Option<String>,
    pub avatar_url: Option<String>,
    pub agent_start: String,
    pub is_public: i64,
    pub excluded_repos: String,
}

/// The `u/<login>/current.json` version pointer the analyzer writes last.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CurrentPointer {
    pub v: u32,
}
