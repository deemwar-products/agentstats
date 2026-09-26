package analyzer

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Repo identifies one bare repository to walk.
type Repo struct {
	// GitDir is the path to a local bare clone (*.git). Used by the CLI and
	// tests. The container populates this after cloning with the user token.
	GitDir string
	// Name is the short repo name shown in the dropped-commit list.
	Name string
	// Source is "github" or "gitlab" (the product is github-only; gitlab is
	// only ever present in local prototype data).
	Source string
}

// Analyzer runs the counting rules over a set of bare repos.
type Analyzer struct {
	// Era explains the boundary when AgentStart was "auto" (set by Run).
	Era EraPick

	// OnStep, when set, reports live progress: phase is "scanning" (parallel git log)
	// or "counting" (folding), with running totals of your commits and honest lines.
	OnStep func(phase, repo string, done, total, commits, lines int)

	Repos      []Repo
	Emails     map[string]bool // lowercased author emails that count as "me"
	AgentStart string          // YYYY-MM; months >= this are the "after" era

	// AgentRules is the commit-signature → tool table (analyzer/agents.json).
	// When nil, agent attribution is skipped.
	AgentRules []AgentRule

	// PrivateRepos holds the Repo.Name of repositories that are private. Their
	// names are redacted to "a private repo" in the dropped/excluded output so
	// a private repo name never reaches a public card or page (docs 07).
	PrivateRepos map[string]bool
}

// repoLabel returns the repo name to show in output, redacting private repos.
func (a *Analyzer) repoLabel(name string) string {
	if a.PrivateRepos[name] {
		return "a private repo"
	}
	return name
}

type progressFn func(repo string, done, total int)

type bufRow struct {
	month string
	lang  string
	added int
}

// runState holds every accumulator for one analysis run.
type runState struct {
	keepPrefetch bool                 // probe pass: read prefetched output without consuming it
	prefetch     map[string]gitResult // git log output fetched in parallel by Run; consumed once
	a            *Analyzer

	seen       map[string]bool           // deduped authored SHAs
	yc         map[string]int            // commits by year (incl. dropped)
	yl         map[string]int            // counted lines by year (excl. dropped)
	ml         map[string]int            // counted lines by month
	langYear   map[string]map[string]int // year -> lang -> lines
	langEra    map[string]map[string]int // era -> lang -> lines
	linesEra   map[string]int            // era -> counted lines
	commitsEra map[string]int            // era -> commits (incl. dropped)
	reposEra   map[string]map[string]bool
	dropped    []DroppedCommit

	countedLines map[string]int        // sha -> counted (non-dropped) lines
	sig          map[string]*commitSig // sha -> detected signature (attribution)

	ghCount, glCount int
}

func newRunState(a *Analyzer) *runState {
	return &runState{
		a:            a,
		seen:         map[string]bool{},
		yc:           map[string]int{},
		yl:           map[string]int{},
		ml:           map[string]int{},
		langYear:     map[string]map[string]int{},
		langEra:      map[string]map[string]int{"before": {}, "after": {}},
		linesEra:     map[string]int{"before": 0, "after": 0},
		commitsEra:   map[string]int{"before": 0, "after": 0},
		reposEra:     map[string]map[string]bool{"before": {}, "after": {}},
		countedLines: map[string]int{},
		sig:          map[string]*commitSig{},
	}
}

// Run walks every repo and returns the aggregate Stats. It is deterministic:
// repos are processed in slice order and commits are deduped by SHA across
// repos (the first repo to carry a SHA claims it for the dropped list).
func (a *Analyzer) Run(login string, progress progressFn) (*Stats, error) {
	rs := newRunState(a)
	total := len(a.Repos)
	if total > 1 {
		rs.prefetchLogs(a.Repos, len(a.AgentRules) > 0)
	}
	if a.AgentStart == "" || a.AgentStart == AutoEra {
		// Probe pass over the same (prefetched) history: monthly lines only, no era split,
		// then pick the boundary from the user's own curve against the AI release timeline.
		probe := *a
		probe.AgentStart, probe.AgentRules, probe.OnStep = "9999-12", nil, nil
		prs := newRunState(&probe)
		prs.prefetch, prs.keepPrefetch = rs.prefetch, true
		for _, r := range a.Repos {
			if err := prs.walkRepo(r); err != nil {
				return nil, fmt.Errorf("repo %s (era probe): %w", r.Name, err)
			}
		}
		a.Era = DetectEra(prs.ml, Milestones())
		a.AgentStart = a.Era.Month
	}
	for i, r := range a.Repos {
		if r.Source == "gitlab" {
			rs.glCount++
		} else {
			rs.ghCount++
		}
		if err := rs.walkRepo(r); err != nil {
			return nil, fmt.Errorf("repo %s: %w", r.Name, err)
		}
		if len(a.AgentRules) > 0 {
			if err := rs.attributeRepo(r); err != nil {
				return nil, fmt.Errorf("repo %s (attribution): %w", r.Name, err)
			}
		}
		if progress != nil {
			progress(r.Name, i+1, total)
		}
		if a.OnStep != nil {
			a.OnStep("counting", a.repoLabel(r.Name), i+1, total, len(rs.seen), sumInts(rs.yl))
		}
	}
	return rs.assemble(login), nil
}

// walkRepo runs the numstat pass for one repo, folding it into the accumulators.
func (rs *runState) walkRepo(r Repo) error {
	out, err := rs.gitOut(r, numstatArgs(r))
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)

	var curMonth string // "" => commit not counted (wrong author or duplicate)
	var curSHA, curFull string
	var buf []bufRow
	var nf int

	flush := func() {
		if curMonth != "" && len(buf) > 0 {
			tot := 0
			for _, b := range buf {
				tot += b.added
			}
			if nf > MaxCommitFiles || tot > MaxCommitLines {
				rs.dropped = append(rs.dropped, DroppedCommit{
					Month: curMonth, Repo: rs.a.repoLabel(r.Name), SHA: curSHA, Files: nf, Lines: tot,
				})
			} else {
				era := rs.a.era(curMonth)
				for _, b := range buf {
					y := b.month[:4]
					rs.yl[y] += b.added
					rs.ml[b.month] += b.added
					if rs.langYear[y] == nil {
						rs.langYear[y] = map[string]int{}
					}
					rs.langYear[y][b.lang] += b.added
					rs.langEra[era][b.lang] += b.added
				}
				rs.linesEra[era] += tot
				rs.reposEra[era][r.Name] = true
				rs.countedLines[curFull] += tot
			}
		}
		buf = buf[:0]
		nf = 0
	}

	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "@@") {
			flush()
			parts := strings.SplitN(line[2:], "|", 3)
			if len(parts) < 3 {
				curMonth = ""
				continue
			}
			h, e, d := parts[0], parts[1], parts[2]
			curMonth = ""
			if rs.a.Emails[strings.ToLower(e)] && !rs.seen[h] {
				rs.seen[h] = true
				curFull = h
				curSHA = h[:min(8, len(h))]
				curMonth = d
				rs.yc[d[:4]]++
				rs.commitsEra[rs.a.era(d)]++
			}
			continue
		}
		if curMonth == "" || line == "" {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) < 3 || p[0] == "-" {
			continue
		}
		f := p[2]
		nf++
		if strings.Contains(f, "=>") {
			continue
		}
		if SKIP.MatchString(f) {
			continue
		}
		ext := extOf(f)
		lang, ok := LANG[ext]
		if !ok || NOTCODE[lang] {
			continue
		}
		added, err := strconv.Atoi(p[0])
		if err != nil {
			continue
		}
		if added > MaxFileAdded {
			continue
		}
		buf = append(buf, bufRow{month: curMonth, lang: lang, added: added})
	}
	flush()
	return sc.Err()
}

// numstatArgs is the git log pass that feeds walkRepo.
func numstatArgs(r Repo) []string {
	return []string{"--git-dir", r.GitDir, "log", "--all", "--no-merges", "--numstat",
		"--format=@@%H|%ae|%ad", "--date=format:%Y-%m"}
}

// gitOut returns git's output for args, from the parallel prefetch when Run made one.
func (rs *runState) gitOut(r Repo, args []string) ([]byte, error) {
	key := strings.Join(args, "\x00")
	if rs.prefetch != nil {
		if v, ok := rs.prefetch[key]; ok {
			if !rs.keepPrefetch {
				delete(rs.prefetch, key) // free memory as soon as it is folded
			}
			return v.out, v.err
		}
	}
	return exec.Command("git", args...).Output()
}

type gitResult struct {
	out []byte
	err error
}

// LogWorkers is how many repos' git log passes run at once. Folding into the
// accumulators stays sequential and in repo order, so dedupe-by-SHA is unchanged.
var LogWorkers = max(4, 2*runtime.NumCPU())

// prefetchLogs runs every repo's git log passes in parallel and keeps the output in memory.
func (rs *runState) prefetchLogs(repos []Repo, withAgents bool) {
	type job struct {
		key, repo string
		args      []string
	}
	var scanned int64
	var jobs []job
	for _, r := range repos {
		a := numstatArgs(r)
		jobs = append(jobs, job{key: strings.Join(a, "\x00"), repo: r.Name, args: a})
		if withAgents {
			b := attributionArgs(r)
			jobs = append(jobs, job{key: strings.Join(b, "\x00"), repo: r.Name, args: b})
		}
	}
	res := make([]gitResult, len(jobs))
	idx := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < LogWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				out, err := exec.Command("git", jobs[i].args...).Output()
				res[i] = gitResult{out, err}
				if rs.a.OnStep != nil {
					n := int(atomic.AddInt64(&scanned, 1))
					rs.a.OnStep("scanning", rs.a.repoLabel(jobs[i].repo), n, len(jobs), -1, -1)
				}
			}
		}()
	}
	for i := range jobs {
		idx <- i
	}
	close(idx)
	wg.Wait()
	rs.prefetch = make(map[string]gitResult, len(jobs))
	for i, j := range jobs {
		rs.prefetch[j.key] = res[i]
	}
}

// era returns "before" or "after" for a YYYY-MM month.
func (a *Analyzer) era(month string) string {
	if month >= a.AgentStart {
		return "after"
	}
	return "before"
}

// extOf mirrors stats.py: the lowercased extension only when the basename
// contains a dot, else "".
func extOf(f string) string {
	base := f
	if i := strings.LastIndex(f, "/"); i >= 0 {
		base = f[i+1:]
	}
	if !strings.Contains(base, ".") {
		return ""
	}
	i := strings.LastIndex(f, ".")
	return strings.ToLower(f[i+1:])
}

func (rs *runState) assemble(login string) *Stats {
	a := rs.a
	s := &Stats{
		Login:         login,
		AgentStart:    a.AgentStart,
		Era:           eraFor(a),
		CommitsByYear: rs.yc,
		LinesByYear:   rs.yl,
		LangByYear:    rs.langYear,
		ReposGithub:   rs.ghCount,
		ReposGitlab:   rs.glCount,
		Dropped:       rs.dropped,
		Months:        rs.ml,
		Years:         map[string]YearStat{},
	}

	firstYear, lastYear := "", ""
	totalLines, totalCommits := 0, 0
	for y, c := range rs.yc {
		yy := s.Years[y]
		yy.Commits = c
		s.Years[y] = yy
		totalCommits += c
		if firstYear == "" || y < firstYear {
			firstYear = y
		}
		if y > lastYear {
			lastYear = y
		}
	}
	for y, l := range rs.yl {
		yy := s.Years[y]
		yy.Lines = l
		s.Years[y] = yy
		totalLines += l
	}
	s.FirstYear = firstYear
	s.Totals = Totals{Lines: totalLines, Commits: totalCommits, Repos: rs.ghCount + rs.glCount}

	agentYear := a.AgentStart[:4]
	bestBeforeYear, bestBeforeLines := "", -1
	for y, l := range rs.yl {
		if y < agentYear && l > bestBeforeLines {
			bestBeforeLines, bestBeforeYear = l, y
		}
	}

	peakMonth, peakLines := "", -1
	for m, l := range rs.ml {
		if l > peakLines {
			peakLines, peakMonth = l, m
		}
	}
	s.PeakMonth = PeakMonth{Month: peakMonth, Lines: peakLines, BeatsBestYear: bestBeforeYear}

	s.Eras.Before = rs.buildEra("before", agentYear, firstYear, lastYear, false)
	s.Eras.After = rs.buildEra("after", agentYear, firstYear, lastYear, true)

	// Excluded summary — sort dropped by lines desc, top 3.
	sortedDrop := append([]DroppedCommit(nil), rs.dropped...)
	sort.Slice(sortedDrop, func(i, j int) bool { return sortedDrop[i].Lines > sortedDrop[j].Lines })
	s.Dropped = sortedDrop
	exLines := 0
	for _, d := range sortedDrop {
		exLines += d.Lines
	}
	s.Excluded = Excluded{Commits: len(sortedDrop), Lines: exLines}
	for i := 0; i < len(sortedDrop) && i < 3; i++ {
		d := sortedDrop[i]
		s.Excluded.Top = append(s.Excluded.Top, DroppedSummary{
			Repo: strings.TrimSuffix(d.Repo, ".git"), SHA: d.SHA, Files: d.Files, Lines: d.Lines, Reason: "bulk-import",
		})
	}

	if len(a.AgentRules) > 0 {
		s.Agents = rs.buildAgents()
	}
	if s.SkippedRepos == nil {
		s.SkippedRepos = []SkippedRepo{}
	}
	return s
}

func (rs *runState) buildEra(era, agentYear, firstYear, lastYear string, after bool) Era {
	e := Era{
		Lines:        rs.linesEra[era],
		Commits:      rs.commitsEra[era],
		ReposTouched: len(rs.reposEra[era]),
		Langs:        rs.langEra[era],
	}
	if e.Langs == nil {
		e.Langs = map[string]int{}
	}
	if e.Commits > 0 {
		e.LinesPerCommit = int(float64(e.Lines)/float64(e.Commits) + 0.5)
	}
	if e.Lines > 0 {
		for _, v := range e.Langs {
			if float64(v)/float64(e.Lines) >= DailyLangThreshold {
				e.DailyLangs++
			}
		}
	}
	if after {
		e.From = agentYear
		e.To = lastYear
		e.PaceThisYear = rs.yl[lastYear]
	} else {
		e.From = firstYear
		beforeTo := ""
		for y := range rs.yl {
			if y < agentYear && y > beforeTo {
				beforeTo = y
			}
		}
		if beforeTo == "" {
			beforeTo = firstYear
		}
		e.To = beforeTo
		fy, _ := strconv.Atoi(firstYear)
		ty, _ := strconv.Atoi(beforeTo)
		span := ty - fy + 1
		if span < 1 {
			span = 1
		}
		e.PacePerYear = e.Lines / span
	}
	return e
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sumInts(m map[string]int) int {
	t := 0
	for _, v := range m {
		t += v
	}
	return t
}

func eraFor(a *Analyzer) *EraPick {
	if a.Era.Month == "" {
		return &EraPick{Month: a.AgentStart}
	}
	e := a.Era
	return &e
}
