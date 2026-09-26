package web

// cards.go holds the UI view-models for the server-rendered pages (docs/ux/
// index.html is the approved design). Everything a template shows is computed
// here from analyzer.Stats so the templates stay dumb: card 1 (before/after),
// card 2 (language mix + monthly chart with AI-release ticks), the agent
// attribution panel and the exclusions table. The monthly chart is inline SVG
// built in Go; its colours are CSS variables so it follows the page theme.

import (
	"fmt"
	"html/template"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/render"
	"github.com/deemwar-products/agentstats/analyzer/store"
)

// installURL is where a signed-in user installs the GitHub App on their repos.
const installURL = "https://github.com/apps/agentstats-by-deemwar/installations/new"

// langColors is the fixed colour per language (the mockup's LANGC map, extended
// for languages the mockup did not show). A language keeps its colour across
// both eras so "JavaScript → Go" reads at a glance.
var langColors = map[string]string{
	"Go": "#2f8f9d", "JavaScript": "#e0a526", "TypeScript": "#4a6fa5", "Java": "#c8553d",
	"Python": "#3f7d5c", "HTML": "#d8843b", "CSS": "#8a6bb0", "Groovy": "#b0657f",
	"Ruby": "#9b3b4a", "Rust": "#b5673a", "Shell": "#6f8f4e", "SQL": "#c29a3c",
	"C#": "#5b7f3a", "C": "#6d6a8a", "C++": "#a0527a", "Kotlin": "#7a64b8",
	"Swift": "#d4623e", "PHP": "#5d6aa0", "Clojure": "#4f8a6e", "Scala": "#a8443c",
	"Dart": "#2e8c8c", "Elixir": "#6e4a7e", "Vue": "#4f9a6f", "Svelte": "#c8603a",
	"Other": "#a59d91",
}

var fallbackLangColors = []string{"#7d8f69", "#9a7b5b", "#5f7d95", "#a86f8f", "#8c8a4f", "#6b6f9c"}

func langColor(name string) string {
	if c, ok := langColors[name]; ok {
		return c
	}
	h := 0
	for _, c := range name {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return fallbackLangColors[h%len(fallbackLangColors)]
}

// ---- view-model types ----

type kv struct{ K, V string }

type eraView struct {
	Label, Span, Big string
	Rows             []kv
}

type card1View struct {
	Kicker, Headline     string
	Before, After        eraView
	CalloutLead, Callout string
	Excluded             string
}

type langSeg struct {
	Name  string
	Color template.CSS
	Pct   int
}

type card2View struct {
	Headline                string
	BeforeLabel, AfterLabel string
	BeforeLangs, AfterLangs []langSeg
	ChartKicker, ChartNote  string
	Chart                   template.HTML
	Pairs                   []kv
}

type toolRow struct {
	Name, Val string
	Color     template.CSS
	Width     int
}

type agentsView struct {
	Has      bool
	Headline string
	Rows     []toolRow
	TopModel string
}

type exclRow struct{ Tag, Where, Lines string }

type exclView struct {
	Total string
	Rows  []exclRow
	More  int
}

// dashView is everything the dashboard / public profile shows beyond the
// handler's own fields.
type dashView struct {
	Card1         card1View
	Card2         card2View
	Agents        agentsView
	Excl          exclView
	Timeline      timelineView
	Initial       string
	AvatarURL     string
	Repos         int
	Updated       string
	AgentStartTxt string
	NeedsInstall  bool
	InstallURL    string
}

// ---- builders ----

func buildDash(st *analyzer.Stats) *dashView {
	return &dashView{
		Card1:         buildCard1(st),
		Card2:         buildCard2(st),
		Agents:        buildAgents(st),
		Excl:          buildExcl(st),
		Timeline:      buildTimeline(st),
		Repos:         st.Totals.Repos,
		AgentStartTxt: monthShort(st.AgentStart),
		InstallURL:    installURL,
	}
}

func buildCard1(st *analyzer.Stats) card1View {
	b, a := st.Eras.Before, st.Eras.After
	years := b.YearCount()
	months := monthsBetween(st.AgentStart, lastMonthOf(st))
	c := card1View{
		Kicker:   fmt.Sprintf("%s lines · %s commits · %s–%s", render.FmtK(st.Totals.Lines), commaInt(st.Totals.Commits), st.FirstYear, a.To),
		Headline: render.Card1Headline(st),
		Before: eraView{Label: "Before agents", Span: fmt.Sprintf("%s · %s", b.Label(), plural(years, "year")),
			Big: render.FmtK(b.Lines)},
		After: eraView{Label: "After agents", Span: fmt.Sprintf("%s · %s", a.Label(), plural(months, "month")),
			Big: render.FmtK(a.Lines)},
		Excluded: render.FmtK(st.Excluded.Lines),
	}
	c.Before.Rows = eraRows(b, fmt.Sprintf("%s / year", render.FmtK(b.PacePerYear)))
	c.After.Rows = eraRows(a, fmt.Sprintf("%s in %s", render.FmtK(a.PaceThisYear), a.To))

	bestYear, best := bestBeforeYear(st)
	c.CalloutLead = "The pace changed, not the hours."
	parts := []string{}
	if years > 0 && b.PacePerYear > 0 {
		parts = append(parts, fmt.Sprintf("%s averaged %s lines a year.", capFirst(numberWord(years))+" "+pluralWord(years, "year"), render.FmtK(b.PacePerYear)))
	}
	if st.PeakMonth.Month != "" {
		s := fmt.Sprintf("%s alone shipped %s", monthLong(st.PeakMonth.Month), render.FmtK(st.PeakMonth.Lines))
		if best > 0 {
			r := float64(st.PeakMonth.Lines) / float64(best)
			switch {
			case r >= 2:
				s += fmt.Sprintf(", %s my best full year (%s in %s)", timesTxt(r), render.FmtK(best), bestYear)
			case r >= 1:
				s += fmt.Sprintf(", more than my best full year (%s in %s)", render.FmtK(best), bestYear)
			}
		}
		parts = append(parts, s+".")
	}
	c.Callout = strings.Join(parts, " ")
	return c
}

func eraRows(e analyzer.Era, pace string) []kv {
	rows := []kv{}
	if top := topLangShares(e.Langs, 1); len(top) > 0 && top[0].Name != "Other" {
		rows = append(rows, kv{top[0].Name, fmt.Sprintf("%s · %d%%", render.FmtK(e.Langs[top[0].Name]), top[0].Pct)})
	}
	rows = append(rows, kv{"Commits", commaInt(e.Commits)}, kv{"Pace", pace})
	if n := langsCovering(e.Langs, 0.9); n > 0 {
		rows = append(rows, kv{"Languages", fmt.Sprintf("%d cover 90%%", n)})
	}
	return rows
}

func buildCard2(st *analyzer.Stats) card2View {
	b, a := st.Eras.Before, st.Eras.After
	c := card2View{
		Headline:    render.Card2Headline(st),
		BeforeLabel: fmt.Sprintf("%s: %s", b.Label(), mixLabel(b.Langs)),
		AfterLabel:  fmt.Sprintf("%s: %s", a.Label(), mixLabel(a.Langs)),
		BeforeLangs: topLangShares(b.Langs, 6),
		AfterLangs:  topLangShares(a.Langs, 6),
		ChartKicker: "Lines per month since " + monthShort(st.AgentStart),
		Chart:       template.HTML(monthlyChart(st, analyzer.Milestones())),
	}
	if st.PeakMonth.Month != "" {
		c.ChartNote = fmt.Sprintf("The highlighted bar is %s: %s lines in one month. Ticks mark AI releases.", monthLong(st.PeakMonth.Month), render.FmtK(st.PeakMonth.Lines))
	}
	months := monthsBetween(st.AgentStart, lastMonthOf(st))
	c.Pairs = []kv{
		{fmt.Sprintf("%s → %s", commaInt(b.LinesPerCommit), commaInt(a.LinesPerCommit)), "lines per commit"},
		{fmt.Sprintf("%s → %s", commaInt(b.Commits), commaInt(a.Commits)), fmt.Sprintf("commits (%d yrs → %d mo)", b.YearCount(), months)},
		{fmt.Sprintf("%s → %s", render.FmtK(b.PacePerYear), render.FmtK(a.PaceThisYear)), "lines per year"},
	}
	return c
}

var toolColors = map[string]string{
	"Claude Code": "var(--l1)", "GitHub Copilot": "var(--l4)", "OpenAI Codex": "var(--l3)",
	"Devin": "var(--l5)", "Cursor": "var(--l2)", "Aider": "var(--l6)",
}

func buildAgents(st *analyzer.Stats) agentsView {
	v := agentsView{}
	if st.Agents == nil || len(st.Agents.ByTool) == 0 {
		v.Headline = "No agent-signed commits found"
		return v
	}
	v.Has = true
	v.Headline = fmt.Sprintf("At least %.0f%% of agent-era lines are agent-signed", st.Agents.ShareOfAfterEra*100)
	type tc struct {
		name string
		ts   *analyzer.ToolStat
	}
	var list []tc
	models := map[string]int{}
	for n, ts := range st.Agents.ByTool {
		if ts == nil {
			continue
		}
		list = append(list, tc{n, ts})
		for m, c := range ts.Models {
			models[m] += c
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].ts.Commits != list[j].ts.Commits {
			return list[i].ts.Commits > list[j].ts.Commits
		}
		return list[i].name < list[j].name
	})
	max := 1
	if len(list) > 0 && list[0].ts.Commits > 0 {
		max = list[0].ts.Commits
	}
	spare := []string{"var(--l6)", "var(--l2)", "var(--lo)"}
	for i, t := range list {
		col, ok := toolColors[t.name]
		if !ok {
			col = spare[i%len(spare)]
		}
		w := int(math.Round(float64(t.ts.Commits) / float64(max) * 100))
		if w < 2 {
			w = 2
		}
		v.Rows = append(v.Rows, toolRow{Name: t.name, Color: template.CSS(col), Width: w, Val: commaInt(t.ts.Commits) + " commits"})
	}
	best, bc := "", 0
	for m, c := range models {
		if c > bc || (c == bc && m < best) {
			best, bc = m, c
		}
	}
	v.TopModel = best
	return v
}

func buildExcl(st *analyzer.Stats) exclView {
	v := exclView{Total: render.FmtK(st.Excluded.Lines)}
	for _, d := range st.Excluded.Top {
		// Private repo names are already redacted to "a private repo" by the
		// analyzer; never fall back to the SHA-only raw Dropped list here.
		where := d.Repo
		if d.Files > 0 {
			where += " · " + commaInt(d.Files) + " files"
		}
		v.Rows = append(v.Rows, exclRow{Tag: strings.ReplaceAll(d.Reason, "-", " "), Where: where, Lines: render.FmtK(d.Lines)})
	}
	if more := st.Excluded.Commits - len(st.Excluded.Top); more > 0 {
		v.More = more
	}
	return v
}

// ---- monthly chart (inline SVG) ----

type tick struct {
	idx   int
	label string
}

// monthlyChart draws lines-per-month bars from the agent-era start to the last
// month with data, the peak bar in the accent colour, and AI-release ticks
// staggered onto rows so labels never overlap.
func monthlyChart(st *analyzer.Stats, miles []analyzer.Milestone) string {
	start := st.AgentStart
	end := lastMonthOf(st)
	keys := monthRange(start, end)
	if len(keys) == 0 {
		return ""
	}
	const W, H, pad = 900.0, 230.0, 24.0
	bw := (W - pad*2) / float64(len(keys))
	max := 0
	for _, k := range keys {
		if st.Months[k] > max {
			max = st.Months[k]
		}
	}
	if max == 0 {
		max = 1
	}

	// Group milestones by month inside the range.
	inRange := map[string]int{}
	for i, k := range keys {
		inRange[k] = i
	}
	byMonth := map[int][]string{}
	for _, m := range miles {
		if len(m.Date) < 7 {
			continue
		}
		if i, ok := inRange[m.Date[:7]]; ok {
			byMonth[i] = append(byMonth[i], shortTool(m.Tool))
		}
	}
	var ticks []tick
	for i, names := range byMonth {
		lbl := strings.Join(dedupe(names), " · ")
		if len(lbl) > 30 {
			lbl = lbl[:29] + "…"
		}
		ticks = append(ticks, tick{i, lbl})
	}
	sort.Slice(ticks, func(a, b int) bool { return ticks[a].idx < ticks[b].idx })

	// Greedy row assignment: a label goes on the first row whose last label
	// ends before this one starts.
	const rowH, charW = 13.0, 5.9
	var rowEnd []float64
	rows := make([]int, len(ticks))
	for ti, t := range ticks {
		x := pad + float64(t.idx)*bw + bw/2
		w := float64(len([]rune(t.label)))*charW + 10
		r := 0
		for ; r < len(rowEnd); r++ {
			if rowEnd[r] < x-2 {
				break
			}
		}
		if r == len(rowEnd) {
			rowEnd = append(rowEnd, 0)
		}
		rowEnd[r] = x + w
		rows[ti] = r
	}
	top := float64(len(rowEnd))*rowH + 22 // room for tick labels + peak label
	if top < 30 {
		top = 30
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="Lines per month since %s" style="overflow:visible">`, W, H+34, esc(monthShort(start)))
	for ti, t := range ticks {
		x := pad + float64(t.idx)*bw + bw/2
		ty := 12 + float64(rows[ti])*rowH
		anchor, tx := "start", x+3
		if x+float64(len([]rune(t.label)))*charW > W {
			anchor, tx = "end", x-3
		}
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.0f" style="stroke:var(--ink-3)" stroke-dasharray="2 3" stroke-width="1"/>`, x, x, ty+4, H)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="10.5" text-anchor="%s" style="fill:var(--ink-2)">%s</text>`, tx, ty, anchor, esc(t.label))
	}
	for i, k := range keys {
		v := st.Months[k]
		h := float64(v) / float64(max) * (H - top)
		if h < 2 {
			h = 2
		}
		x := pad + float64(i)*bw
		peak := v == max && v > 0
		fill := "var(--bar)"
		if peak {
			fill = "var(--accent)"
		}
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2" style="fill:%s"><title>%s: %s lines</title></rect>`,
			x+2, H-h, math.Max(bw-4, 1), h, fill, monthShort(k), commaInt(v))
		if peak {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" font-size="11" font-weight="700" style="fill:var(--ink)">%s</text>`, x+bw/2, H-h-6, render.FmtK(v))
		}
	}
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="11" style="fill:var(--ink-3)">%s</text>`, pad, H+22, esc(monthShort(keys[0])))
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="11" text-anchor="end" style="fill:var(--ink-3)">%s</text>`, W-pad, H+22, esc(monthShort(keys[len(keys)-1])))
	b.WriteString(`</svg>`)
	return b.String()
}

func shortTool(t string) string {
	if i := strings.Index(t, " / "); i > 0 {
		t = t[:i]
	}
	t = strings.TrimPrefix(t, "OpenAI ")
	t = strings.TrimPrefix(t, "GitHub ")
	return t
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---- landing ----

// repoURL is the public source of this site.
const repoURL = "https://github.com/deemwar-products/agentstats"

type landingView struct {
	Featured string     // login whose real, public card the landing shows
	Sample   *card1View // nil when that profile is private or not analysed: no fake numbers
	Repo     string
}

// featuredLogin is the public profile shown on the landing page as a real example.
const featuredLogin = "muthuishere"

func (s *Server) landingView() landingView {
	v := landingView{Featured: featuredLogin, Repo: repoURL}
	if u, err := s.store.GetUserByLogin(featuredLogin); err == nil && u.IsPublic {
		if st, ok := s.statsFor(u.GithubID); ok {
			c := buildCard1(st)
			v.Sample = &c
		}
	}
	return v
}

// ---- share + meta ----

type shareLinks struct{ LinkedIn, X, WhatsApp string }

func makeShareLinks(profileURL, text string) shareLinks {
	return shareLinks{
		LinkedIn: "https://www.linkedin.com/sharing/share-offsite/?url=" + url.QueryEscape(profileURL),
		X:        "https://x.com/intent/post?text=" + url.QueryEscape(text) + "&url=" + url.QueryEscape(profileURL),
		WhatsApp: "https://wa.me/?text=" + url.QueryEscape(text+" "+profileURL),
	}
}

// dashViewFor builds the dashboard view for a user, including metadata the
// Stats alone do not carry (avatar, last update, install state).
func (s *Server) dashViewFor(u store.User, st *analyzer.Stats, computedAt string, noInstallHint bool) *dashView {
	v := buildDash(st)
	v.AvatarURL = u.AvatarURL
	name := u.Name
	if name == "" {
		name = u.Login
	}
	v.Initial = strings.ToUpper(firstRune(name))
	v.Updated = agoTxt(computedAt)
	if u.AgentStart != "" && u.AgentStart != analyzer.AutoEra {
		v.AgentStartTxt = monthShort(u.AgentStart)
	}
	if ic, ok := s.store.(interface {
		HasInstallation(githubID int64) (installed, known bool)
	}); ok {
		if inst, known := ic.HasInstallation(u.GithubID); known && !inst {
			v.NeedsInstall = true
		}
	} else if s.devMode && noInstallHint {
		v.NeedsInstall = true
	}
	return v
}

// ---- small helpers ----

func commaInt(n int) string {
	if n < 0 {
		return "-" + commaInt(-n)
	}
	s := strconv.Itoa(n)
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func plural(n int, unit string) string {
	return strconv.Itoa(n) + " " + pluralWord(n, unit)
}

func pluralWord(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return unit + "s"
}

func numberWord(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
		"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen", "twenty"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return strconv.Itoa(n)
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func timesTxt(r float64) string {
	if r >= 10 || math.Abs(r-math.Round(r)) < 0.1 {
		return fmt.Sprintf("%.0f×", math.Round(r))
	}
	return fmt.Sprintf("%.1f×", r)
}

func yearsSpan(from, to string) int {
	f, _ := strconv.Atoi(from)
	t, _ := strconv.Atoi(to)
	if f == 0 || t < f {
		return 0
	}
	return t - f + 1
}

func parseYM(s string) (int, int) {
	if len(s) < 7 {
		return 0, 0
	}
	y, _ := strconv.Atoi(s[:4])
	m, _ := strconv.Atoi(s[5:7])
	return y, m
}

func monthsBetween(start, end string) int {
	sy, sm := parseYM(start)
	ey, em := parseYM(end)
	n := (ey*12 + em) - (sy*12 + sm) + 1
	if n < 1 {
		return 1
	}
	return n
}

func monthRange(start, end string) []string {
	sy, sm := parseYM(start)
	ey, em := parseYM(end)
	if sy == 0 || ey == 0 {
		return nil
	}
	var out []string
	for y, m := sy, sm; y*12+m <= ey*12+em && len(out) < 240; {
		out = append(out, fmt.Sprintf("%04d-%02d", y, m))
		m++
		if m > 12 {
			m, y = 1, y+1
		}
	}
	return out
}

func lastMonthOf(st *analyzer.Stats) string {
	last := ""
	for k := range st.Months {
		if k > last {
			last = k
		}
	}
	if last == "" {
		last = st.PeakMonth.Month
	}
	if last == "" || last < st.AgentStart {
		last = st.AgentStart
	}
	return last
}

var monthAbbr = []string{"", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
var monthFull = []string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

func monthShort(ym string) string {
	y, m := parseYM(ym)
	if m < 1 || m > 12 {
		return ym
	}
	return fmt.Sprintf("%s %d", monthAbbr[m], y)
}

func monthLong(ym string) string {
	y, m := parseYM(ym)
	if m < 1 || m > 12 {
		return ym
	}
	return fmt.Sprintf("%s %d", monthFull[m], y)
}

func bestBeforeYear(st *analyzer.Stats) (string, int) {
	cut := st.AgentStart
	if len(cut) >= 4 {
		cut = cut[:4]
	}
	by, best := "", 0
	for y, ys := range st.Years {
		if y < cut && ys.Lines > best {
			by, best = y, ys.Lines
		}
	}
	return by, best
}

// topLangShares returns the top-n languages plus an "Other" bucket, with
// whole-number percentages.
func topLangShares(langs map[string]int, n int) []langSeg {
	type lv struct {
		l string
		v int
	}
	total := 0
	var list []lv
	for l, v := range langs {
		total += v
		list = append(list, lv{l, v})
	}
	if total == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].l < list[j].l
	})
	other := 0
	if len(list) > n {
		for _, x := range list[n:] {
			other += x.v
		}
		list = list[:n]
	}
	var out []langSeg
	for _, x := range list {
		out = append(out, langSeg{Name: x.l, Color: template.CSS(langColor(x.l)), Pct: int(math.Round(float64(x.v) / float64(total) * 100))})
	}
	if other > 0 {
		out = append(out, langSeg{Name: "Other", Color: template.CSS(langColor("Other")), Pct: int(math.Round(float64(other) / float64(total) * 100))})
	}
	return out
}

func langsCovering(langs map[string]int, frac float64) int {
	total := 0
	var vs []int
	for _, v := range langs {
		total += v
		vs = append(vs, v)
	}
	if total == 0 {
		return 0
	}
	sort.Sort(sort.Reverse(sort.IntSlice(vs)))
	acc := 0
	for i, v := range vs {
		acc += v
		if float64(acc) >= frac*float64(total) {
			return i + 1
		}
	}
	return len(vs)
}

func mixLabel(langs map[string]int) string {
	top := topLangShares(langs, 2)
	var names []string
	for _, t := range top {
		if t.Name != "Other" {
			names = append(names, t.Name)
		}
	}
	switch len(names) {
	case 0:
		return "no counted code"
	case 1:
		return "mostly " + names[0]
	default:
		return "mostly " + names[0] + " and " + names[1]
	}
}

func agoTxt(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "min") + " ago"
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	default:
		return plural(int(d.Hours()/24), "day") + " ago"
	}
}

func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return "?"
}

func esc(s string) string { return template.HTMLEscapeString(s) }
