package analyzer

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
)

// Per-tool attribution from commit signatures alone (docs 09). We can only see
// what reaches git: bot authors and Co-Authored-By / body trailers. Unsigned
// agent code is indistinguishable from human code, so the card says "at least
// N% agent-signed", never "N% AI".
//
// agents.json and milestones.json are DATA, embedded so the binary/container
// carries them and the site's methodology page can serve them raw. Kept as
// data so adding a tool or a release is a one-line PR.
//
// MILESTONES ARE UNVERIFIED: the dates in milestones.json were drafted from
// memory and each carries a placeholder "source". Every entry needs a
// vendor-announcement URL before launch — an owner/launch step, not the
// analyzer's job. The methodology page renders this file, so a wrong date
// would be public.

//go:embed agents.json
var agentsJSON []byte

//go:embed milestones.json
var milestonesJSON []byte

// AgentRule maps a commit-signature pattern to a tool (analyzer/agents.json).
type AgentRule struct {
	Tool string `json:"tool"`
	// Field to test: "author_name", "author_email", "trailer", "subject", "body".
	Field string `json:"field"`
	// Match is a case-insensitive substring the field must contain.
	Match string `json:"match"`
	// ModelRegex (optional) pulls a model name out of the matched field,
	// e.g. "Claude Opus 4.5" from a Co-Authored-By trailer. Capture group 1.
	ModelRegex string `json:"model_regex,omitempty"`
}

// Milestone is one AI-tooling release marker (analyzer/milestones.json). Dates
// are UNVERIFIED until each carries a vendor-announcement Source URL.
type Milestone struct {
	Date   string `json:"date"` // YYYY-MM-DD
	Tool   string `json:"tool"`
	Event  string `json:"event"`
	Kind   string `json:"kind"` // assistant | chat | model | agent
	Source string `json:"source,omitempty"`
}

// DefaultAgentRules returns the embedded commit-signature → tool table.
func DefaultAgentRules() []AgentRule {
	var rules []AgentRule
	_ = json.Unmarshal(agentsJSON, &rules)
	return rules
}

// Milestones returns the embedded AI-tooling release timeline (unverified).
func Milestones() []Milestone {
	var ms []Milestone
	_ = json.Unmarshal(milestonesJSON, &ms)
	return ms
}

// AgentStats is the `agents` block of the analyzer output.
type AgentStats struct {
	SignedCommits    int                  `json:"signed_commits"`
	SignedLines      int                  `json:"signed_lines"`
	ShareOfAfterEra  float64              `json:"share_of_after_era"`
	ByTool           map[string]*ToolStat `json:"by_tool"`
	FirstSignedMonth string               `json:"first_signed_month"`
}

type ToolStat struct {
	Commits int            `json:"commits"`
	Lines   int            `json:"lines"`
	Models  map[string]int `json:"models,omitempty"`
}

// commitSig is what we detected for one SHA.
type commitSig struct {
	month  string
	tools  map[string]bool
	models map[string]string // tool -> model (when extractable)
}

// attributeRepo runs a second, body-aware git-log pass over one repo and
// records a signature per authored SHA (deduped by SHA in rs.sig).
// attributionArgs is the git log pass that feeds attributeRepo. -z separates commits
// with NUL; \x1f separates fields within a commit.
func attributionArgs(r Repo) []string {
	return []string{"--git-dir", r.GitDir, "log", "--all", "--no-merges", "-z",
		"--format=%H\x1f%ae\x1f%ad\x1f%an\x1f%s\x1f%b\x1f%(trailers:unfold=true)",
		"--date=format:%Y-%m"}
}

func (rs *runState) attributeRepo(r Repo) error {
	out, err := rs.gitOut(r, attributionArgs(r))
	if err != nil {
		return err
	}
	rec := bufio.NewScanner(strings.NewReader(string(out)))
	rec.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	rec.Split(scanNUL)
	for rec.Scan() {
		record := rec.Text()
		if record == "" {
			continue
		}
		f := strings.SplitN(record, "\x1f", 7)
		if len(f) < 7 {
			continue
		}
		sha, email, date, name, subject, body, trailers := f[0], f[1], f[2], f[3], f[4], f[5], f[6]
		if !rs.a.Emails[strings.ToLower(email)] {
			continue
		}
		if _, done := rs.sig[sha]; done {
			continue // deduped by SHA across repos
		}
		fields := map[string]string{
			"author_name": name, "author_email": email,
			"trailer": trailers, "subject": subject, "body": body,
		}
		var sig *commitSig
		for _, rule := range rs.a.AgentRules {
			hay := strings.ToLower(fields[rule.Field])
			if hay == "" || !strings.Contains(hay, strings.ToLower(rule.Match)) {
				continue
			}
			if sig == nil {
				sig = &commitSig{month: date, tools: map[string]bool{}, models: map[string]string{}}
			}
			sig.tools[rule.Tool] = true
			if rule.ModelRegex != "" {
				if re, e := regexp.Compile(rule.ModelRegex); e == nil {
					if m := re.FindStringSubmatch(fields[rule.Field]); len(m) > 1 {
						sig.models[rule.Tool] = strings.TrimSpace(m[1])
					}
				}
			}
		}
		if sig != nil {
			rs.sig[sha] = sig
		}
	}
	return rec.Err()
}

// buildAgents folds the collected signatures into the output block. signed_lines
// uses counted (non-dropped) lines; share_of_after_era is signed after-era lines
// over the after era's counted lines.
func (rs *runState) buildAgents() *AgentStats {
	as := &AgentStats{ByTool: map[string]*ToolStat{}}
	afterSignedLines := 0
	for sha, sig := range rs.sig {
		lines := rs.countedLines[sha] // 0 if the commit was dropped/uncounted
		as.SignedCommits++
		as.SignedLines += lines
		if sig.month != "" {
			if as.FirstSignedMonth == "" || sig.month < as.FirstSignedMonth {
				as.FirstSignedMonth = sig.month
			}
			if rs.a.era(sig.month) == "after" {
				afterSignedLines += lines
			}
		}
		for tool := range sig.tools {
			ts := as.ByTool[tool]
			if ts == nil {
				ts = &ToolStat{Models: map[string]int{}}
				as.ByTool[tool] = ts
			}
			ts.Commits++
			ts.Lines += lines
			if m := sig.models[tool]; m != "" {
				ts.Models[m]++
			}
		}
	}
	for _, ts := range as.ByTool {
		if len(ts.Models) == 0 {
			ts.Models = nil
		}
	}
	if afterLines := rs.linesEra["after"]; afterLines > 0 {
		as.ShareOfAfterEra = float64(afterSignedLines) / float64(afterLines)
	}
	return as
}

// scanNUL is a bufio.SplitFunc that splits on NUL bytes (git -z output).
func scanNUL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == 0 {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
