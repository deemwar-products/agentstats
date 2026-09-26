package web

import (
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/render"
)

// timelineView is the "all your code" line chart plus the per-release pace table and the
// top-5 language ranking shown on the dashboard.
type timelineView struct {
	Chart    template.HTML
	Caption  string
	Releases []releaseRow
	BestLine string
	Langs    []langRankRow
	EraNote  string
}

type releaseRow struct {
	Release, More, AllNames string // primary release, "+2" when several shipped that month, full list for the tooltip
	Date, Pace, Mult        string
	Width                   int // bar width %, relative to the fastest stretch
	Best                    bool
}

type langRankRow struct {
	Rank                             int
	Lang, Color                      string
	Before, BeforePct, After, AfterP string
}

// paceFloor matches the era detector: the "before" pace never counts as less than this.
const paceFloor = 1000.0

func buildTimeline(st *analyzer.Stats) timelineView {
	v := timelineView{}
	months := allMonths(st)
	if len(months) == 0 {
		return v
	}
	ms := analyzer.Milestones()
	sort.Slice(ms, func(i, j int) bool { return ms[i].Date < ms[j].Date })
	v.Chart, v.Caption = cumulativeChart(st, months, ms)
	v.Releases, v.BestLine = releasePace(st, months, ms)
	v.Langs = langRank(st)
	if st.Era != nil && st.Era.Auto && st.Era.Milestone != "" {
		v.EraNote = fmt.Sprintf("Your agent era starts %s: your pace rose %.1f× after %s, the biggest change in your history.",
			monthShort(st.Era.Month), st.Era.Ratio, st.Era.Milestone)
	}
	return v
}

// allMonths is every month from the first month with code to the last, gaps filled.
func allMonths(st *analyzer.Stats) []string {
	first, last := "", ""
	for m, n := range st.Months {
		if n <= 0 {
			continue
		}
		if first == "" || m < first {
			first = m
		}
		if m > last {
			last = m
		}
	}
	if first == "" {
		return nil
	}
	var out []string
	for m := first; m <= last; m = addMonth(m, 1) {
		out = append(out, m)
	}
	return out
}

func addMonth(ym string, n int) string {
	var y, m int
	fmt.Sscanf(ym, "%d-%d", &y, &m)
	t := y*12 + (m - 1) + n
	return fmt.Sprintf("%04d-%02d", t/12, t%12+1)
}

// cumulativeChart draws total honest lines over the whole history as a line, with the AI
// releases marked, and the agent-era boundary shaded.
func cumulativeChart(st *analyzer.Stats, months []string, ms []analyzer.Milestone) (template.HTML, string) {
	const W, H, padL, padR, padT, padB = 900.0, 300.0, 56.0, 16.0, 44.0, 28.0
	cum := make([]int, len(months))
	run := 0
	for i, m := range months {
		run += st.Months[m]
		cum[i] = run
	}
	top := float64(cum[len(cum)-1])
	if top <= 0 {
		return "", ""
	}
	x := func(i int) float64 {
		if len(months) == 1 {
			return padL
		}
		return padL + (W-padL-padR)*float64(i)/float64(len(months)-1)
	}
	y := func(v int) float64 { return padT + (H-padT-padB)*(1-float64(v)/top) }
	idx := map[string]int{}
	for i, m := range months {
		idx[m] = i
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="All your code over time, with AI releases marked" style="overflow:visible">`, W, H)
	// gridlines + y labels
	for _, f := range []float64{0, .25, .5, .75, 1} {
		yy := padT + (H-padT-padB)*(1-f)
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="var(--line)" stroke-width="1"/>`, padL, W-padR, yy, yy)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="end" font-size="11" fill="var(--ink-3)">%s</text>`, padL-8, yy+4, render.FmtK(int(top*f)))
	}
	// agent-era shading
	if i, ok := idx[st.AgentStart]; ok {
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="var(--accent)" opacity=".07"/>`, x(i), padT, W-padR-x(i), H-padT-padB)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11" font-weight="700" fill="var(--accent)">agent era →</text>`, x(i)+6, H-padB-8)
	}
	// release markers: one small vendor badge per release month, hover for the names.
	// Badges go in up to three lanes so neighbouring months never overlap; only the
	// release the agent era starts at gets a text label.
	type mark struct {
		i     int
		names []string
		date  string
	}
	var marks []mark
	for _, m := range ms {
		if len(m.Date) < 7 {
			continue
		}
		i, ok := idx[m.Date[:7]]
		if !ok {
			continue
		}
		if n := len(marks); n > 0 && marks[n-1].i == i {
			marks[n-1].names = append(marks[n-1].names, m.Tool)
			continue
		}
		marks = append(marks, mark{i, []string{m.Tool}, m.Date[:7]})
	}
	const r, gap = 7.0, 16.0
	laneEnd := []float64{-1e9, -1e9, -1e9}
	for _, mk := range marks {
		xx := x(mk.i)
		lane := 0
		for l := range laneEnd {
			if laneEnd[l] <= xx-gap {
				lane = l
				break
			}
			if laneEnd[l] < laneEnd[lane] {
				lane = l
			}
		}
		laneEnd[lane] = xx
		cy := 10 + float64(lane)*(2*r+3)
		v := vendorOf(mk.names[0])
		era := mk.date == st.AgentStart
		tip := template.HTMLEscapeString(strings.Join(mk.names, " · ") + " · " + monthShort(mk.date))
		fmt.Fprintf(&b, `<g class="rel"><title>%s</title>`, tip)
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="var(--ink-3)" stroke-dasharray="2 3" opacity=".6"/>`, xx, xx, cy+r, H-padB)
		stroke := "none"
		if era {
			stroke = "var(--accent)"
		}
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.0f" fill="%s" stroke="%s" stroke-width="2"/>`, xx, cy, r, v.color, stroke)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="middle" font-size="8.5" font-weight="800" fill="#fff">%s</text>`, xx, cy+3, v.letter)
		if len(mk.names) > 1 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="8" fill="var(--ink-3)">+%d</text>`, xx+r+1, cy-3, len(mk.names)-1)
		}
		if era {
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="end" font-size="10.5" font-weight="600" fill="var(--accent)">%s</text>`, xx-r-4, cy+4, template.HTMLEscapeString(mk.names[0]))
		}
		b.WriteString(`</g>`)
	}
	// area + line
	var pts strings.Builder
	for i := range months {
		fmt.Fprintf(&pts, "%.1f,%.1f ", x(i), y(cum[i]))
	}
	fmt.Fprintf(&b, `<polygon points="%.1f,%.1f %s%.1f,%.1f" fill="var(--accent)" opacity=".12"/>`, x(0), H-padB, pts.String(), x(len(months)-1), H-padB)
	fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="var(--accent)" stroke-width="2.4" stroke-linejoin="round"/>`, pts.String())
	// end label + x axis
	fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="4" fill="var(--accent)"/>`, x(len(months)-1), y(cum[len(cum)-1]))
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="10.5" fill="var(--ink-3)">%s</text>`, padL, H-6, monthShort(months[0]))
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="end" font-size="10.5" fill="var(--ink-3)">%s</text>`, W-padR, H-6, monthShort(months[len(months)-1]))
	b.WriteString(`</svg>`)

	caption := ""
	if i, ok := idx[st.AgentStart]; ok && i > 0 {
		before := cum[i-1]
		caption = fmt.Sprintf("%s honest lines in total. %s of them were written before %s; %s since.",
			render.FmtK(int(top)), render.FmtK(before), monthShort(st.AgentStart), render.FmtK(int(top)-before))
	} else {
		caption = fmt.Sprintf("%s honest lines in total.", render.FmtK(int(top)))
	}
	return template.HTML(b.String()), caption
}

// releasePace scores each AI release with the era detector's own measure (analyzer.ReleaseImpacts),
// so the highlighted row, its headline and the era note always agree.
func releasePace(st *analyzer.Stats, months []string, ms []analyzer.Milestone) ([]releaseRow, string) {
	first := months[0]
	var rows []releaseRow
	maxPace, best := 0.0, -1
	for _, ri := range analyzer.ReleaseImpacts(st.Months, ms) {
		if ri.Month <= first {
			continue
		}
		r := releaseRow{Release: ri.Tools[0], AllNames: strings.Join(ri.Tools, " · "), Date: monthShort(ri.Month),
			Pace: render.FmtK(int(ri.AfterPace)) + "/mo"}
		if len(ri.Tools) > 1 {
			r.More = fmt.Sprintf("+%d", len(ri.Tools)-1)
		}
		if ri.Eligible {
			r.Mult = fmt.Sprintf("%.1f×", ri.Ratio)
		} else {
			r.Mult = "too soon"
		}
		if st.Era != nil && st.Era.Month == ri.Month && ri.Eligible {
			best = len(rows)
		}
		maxPace = max(maxPace, ri.AfterPace)
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, ""
	}
	impacts := analyzer.ReleaseImpacts(st.Months, ms)
	k := 0
	for _, ri := range impacts {
		if ri.Month <= first {
			continue
		}
		if maxPace > 0 {
			rows[k].Width = max(3, int(ri.AfterPace*100/maxPace))
		}
		k++
	}
	if best < 0 { // era set by hand: highlight the strongest release instead
		bestR := 0.0
		for i, ri := range impacts[len(impacts)-len(rows):] {
			if ri.Eligible && ri.Ratio > bestR && ri.AfterPace >= paceFloor {
				bestR, best = ri.Ratio, i
			}
		}
	}
	line := ""
	if best >= 0 {
		rows[best].Best = true
		line = fmt.Sprintf("Your biggest jump came after %s (%s): %s your pace in the two years before.", rows[best].Release, rows[best].Date, rows[best].Mult)
	}
	return rows, line
}

// langRank is the top-5 languages of each era, side by side.
func langRank(st *analyzer.Stats) []langRankRow {
	top := func(m map[string]int) []string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool { return m[ks[i]] > m[ks[j]] || (m[ks[i]] == m[ks[j]] && ks[i] < ks[j]) })
		return ks
	}
	b, a := st.Eras.Before, st.Eras.After
	pct := func(v, tot int) string {
		if tot == 0 {
			return "0%"
		}
		return fmt.Sprintf("%d%%", (v*100+tot/2)/tot)
	}
	after := top(a.Langs)
	n := min(5, len(after))
	if n == 0 {
		return nil
	}
	rows := make([]langRankRow, 0, n)
	for i := 0; i < n; i++ {
		l := after[i]
		rows = append(rows, langRankRow{Rank: i + 1, Lang: l, Color: langColor(l),
			Before: render.FmtK(b.Langs[l]), BeforePct: pct(b.Langs[l], b.Lines),
			After: render.FmtK(a.Langs[l]), AfterP: pct(a.Langs[l], a.Lines)})
	}
	return rows
}

type vendor struct{ letter, color, name string }

// vendorOf maps a release to its maker for the chart badge.
func vendorOf(tool string) vendor {
	t := strings.ToLower(tool)
	switch {
	case strings.Contains(t, "copilot"):
		return vendor{"G", "#57606a", "GitHub"}
	case strings.Contains(t, "claude"):
		return vendor{"A", "#c8553d", "Anthropic"}
	case strings.Contains(t, "gpt"), strings.Contains(t, "openai"), strings.Contains(t, "codex"):
		return vendor{"O", "#10a37f", "OpenAI"}
	}
	return vendor{"•", "#8f877d", ""}
}
