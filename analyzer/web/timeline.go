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
	Release, Date, Pace, Mult string
	Width                     int // bar width %, relative to the fastest stretch
	Best                      bool
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
		v.EraNote = fmt.Sprintf("Your agent era starts %s: your pace rose %.1f× after %s, the biggest change in your history. You can change it in Settings.",
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
	// milestone ticks, labels staggered on 3 rows
	row := 0
	for _, m := range ms {
		if len(m.Date) < 7 {
			continue
		}
		i, ok := idx[m.Date[:7]]
		if !ok {
			continue
		}
		ly := 12 + float64(row%3)*13
		row++
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f" stroke="var(--ink-3)" stroke-dasharray="2 3"/>`, x(i), x(i), ly+4, H-padB)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="10.5" fill="var(--ink-2)">%s</text>`, x(i)+3, ly, template.HTMLEscapeString(m.Tool))
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

// releasePace splits the history at each AI release and compares average monthly output
// in each stretch with the stretch before it.
func releasePace(st *analyzer.Stats, months []string, ms []analyzer.Milestone) ([]releaseRow, string) {
	first, last := months[0], months[len(months)-1]
	type cut struct{ month, name string }
	var cuts []cut
	for _, m := range ms {
		if len(m.Date) < 7 || m.Date[:7] <= first || m.Date[:7] > last {
			continue
		}
		if len(cuts) > 0 && cuts[len(cuts)-1].month == m.Date[:7] {
			cuts[len(cuts)-1].name += " · " + m.Tool
			continue
		}
		cuts = append(cuts, cut{m.Date[:7], m.Tool})
	}
	if len(cuts) == 0 {
		return nil, ""
	}
	pace := func(from, to string) (float64, int) { // [from, to)
		sum, n := 0, 0
		for m := from; m < to; m = addMonth(m, 1) {
			sum += st.Months[m]
			n++
		}
		if n == 0 {
			return 0, 0
		}
		return float64(sum) / float64(n), n
	}
	prev, _ := pace(first, cuts[0].month)
	var rows []releaseRow
	maxPace, bestMult, best := 0.0, 0.0, -1
	for i, c := range cuts {
		end := addMonth(last, 1)
		if i+1 < len(cuts) {
			end = cuts[i+1].month
		}
		p, n := pace(c.month, end)
		if n == 0 {
			continue
		}
		mult := ""
		if prev > 0 {
			r := p / max(prev, paceFloor) // a quiet past must not make any bump look huge
			mult = fmt.Sprintf("%.1f×", r)
			if r > bestMult && p >= 1000 {
				bestMult, best = r, len(rows)
			}
		}
		if p > maxPace {
			maxPace = p
		}
		rows = append(rows, releaseRow{Release: c.name, Date: monthShort(c.month), Pace: render.FmtK(int(p)) + "/mo", Mult: mult})
		prev = p
	}
	for i, c := range cuts {
		if i >= len(rows) {
			break
		}
		end := addMonth(last, 1)
		if i+1 < len(cuts) {
			end = cuts[i+1].month
		}
		p, _ := pace(c.month, end)
		if maxPace > 0 {
			rows[i].Width = max(3, int(p*100/maxPace))
		}
	}
	line := ""
	if best >= 0 {
		rows[best].Best = true
		line = fmt.Sprintf("Your biggest jump came after %s: %s your previous pace.", rows[best].Release, rows[best].Mult)
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
