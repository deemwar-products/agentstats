package analyzer

// Stats is the analyzer's output contract — the aggregates-only JSON that D1
// stores and both cards render from. It contains NO code and NO tokens.
// See docs/analyzer-output-schema.md and docs/architecture/04-data-model.md.
type Stats struct {
	Login      string   `json:"login"`
	AgentStart string   `json:"agent_start"`   // YYYY-MM, era boundary
	Era        *EraPick `json:"era,omitempty"` // how the boundary was chosen
	FirstYear  string   `json:"first_year"`
	Totals     Totals   `json:"totals"`
	Eras       Eras     `json:"eras"`

	// Per-year and per-month rollups (counted code lines + commits).
	Years  map[string]YearStat `json:"years"`
	Months map[string]int      `json:"months"` // YYYY-MM -> counted lines

	PeakMonth PeakMonth `json:"peak_month"`
	Excluded  Excluded  `json:"excluded"`

	// Agents is per-tool commit-signature attribution (see docs 09). Omitted
	// when attribution was not requested.
	Agents *AgentStats `json:"agents,omitempty"`

	SkippedRepos []SkippedRepo `json:"skipped_repos"`

	// Reproduction fields — the exact shape prototype/stats.py emits, kept so
	// the regression test can assert against the reference numbers. These are
	// derived from the same counters as the fields above.
	CommitsByYear map[string]int            `json:"commits_by_year"`
	LinesByYear   map[string]int            `json:"lines_by_year"`
	LangByYear    map[string]map[string]int `json:"lang_by_year"`
	ReposGithub   int                       `json:"repos_github"`
	ReposGitlab   int                       `json:"repos_gitlab"`
	Dropped       []DroppedCommit           `json:"dropped"`
}

type Totals struct {
	Lines   int `json:"lines"`
	Commits int `json:"commits"`
	Repos   int `json:"repos"`
}

type Eras struct {
	Before Era `json:"before"`
	After  Era `json:"after"`
}

// Era holds one side of the before/after split. Lines exclude dropped commits;
// Commits include every deduped authored commit in the era (matching stats.py,
// where commits_by_year counts a commit before the bulk-import drop decision).
type Era struct {
	From           string         `json:"from"` // YYYY
	To             string         `json:"to"`   // YYYY
	Lines          int            `json:"lines"`
	Commits        int            `json:"commits"`
	ReposTouched   int            `json:"repos_touched"`
	LinesPerCommit int            `json:"lines_per_commit"`
	Langs          map[string]int `json:"langs"`
	DailyLangs     int            `json:"daily_langs"`      // langs >= DailyLangThreshold of era lines
	PacePerYear    int            `json:"pace_per_year"`    // before era: lines / span years
	PaceThisYear   int            `json:"pace_this_year"`   // after era: lines in the latest year
	Span           string         `json:"span,omitempty"`   // month-aware label, set by NormalizeEras
	Months         int            `json:"months,omitempty"` // months covered, set by NormalizeEras
}

type YearStat struct {
	Lines   int `json:"lines"`
	Commits int `json:"commits"`
}

type PeakMonth struct {
	Month         string `json:"month"`
	Lines         int    `json:"lines"`
	BeatsBestYear string `json:"beats_best_year"` // best pre-agent year it out-produced
}

type Excluded struct {
	Commits int              `json:"commits"`
	Lines   int              `json:"lines"`
	Top     []DroppedSummary `json:"top"`
}

type DroppedSummary struct {
	Repo   string `json:"repo"`
	SHA    string `json:"sha"`
	Files  int    `json:"files"`
	Lines  int    `json:"lines"`
	Reason string `json:"reason"`
}

// DroppedCommit is the raw reproduction record for a bulk-import commit that
// was dropped. NOTE: Repo may name a private repository; the public regression
// fixture stores these generically labelled (r1, r2, …) — see LAUNCH.md.
type DroppedCommit struct {
	Month string `json:"month"`
	Repo  string `json:"repo"`
	SHA   string `json:"sha"` // 8 hex chars
	Files int    `json:"files"`
	Lines int    `json:"lines"`
}

type SkippedRepo struct {
	Repo   string `json:"repo"`
	Reason string `json:"reason"`
}
