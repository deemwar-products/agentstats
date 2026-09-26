package render

import (
	"fmt"
	"strings"

	"github.com/deemwar-products/agentstats/analyzer"
)

const fontStack = "-apple-system,BlinkMacSystemFont,'Segoe UI',Inter,Roboto,Helvetica,Arial,sans-serif"

// footer is the mandatory attribution on every artifact.
func footer(x, y int, t Theme, extra string) string {
	label := "agentstats by deemwar"
	if extra != "" {
		label = extra + " · " + label
	}
	return fmt.Sprintf(`<text x="%d" y="%d" font-size="12" fill="%s" font-family="%s">%s</text>`,
		x, y, t.Muted, fontStack, esc(label))
}

// Card1Headline picks the before/after headline per docs 06.
func Card1Headline(s *analyzer.Stats) string {
	before, after := s.Eras.Before, s.Eras.After
	n := monthsSince(s.AgentStart, lastMonth(s))
	m := before.YearCount()
	switch {
	case after.Lines >= before.Lines:
		return fmt.Sprintf("I wrote more code in the last %s than in the previous %s.", plural(n, "month"), plural(m, "year"))
	case float64(after.Lines) >= 0.5*float64(before.Lines):
		return fmt.Sprintf("I wrote half as much code in the last %s as in the previous %s.", plural(n, "month"), plural(m, "year"))
	default:
		mult := 0.0
		if before.PacePerYear > 0 {
			mult = float64(after.PaceThisYear) / float64(before.PacePerYear)
		}
		return fmt.Sprintf("%s of agents: %s lines, %.1f× my old pace.", plural(n, "month"), fmtK(after.Lines), mult)
	}
}

// BeforeAfter renders card 1 (800×420).
func BeforeAfter(s *analyzer.Stats, t Theme) string {
	const w, h = 800, 420
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="%s">`, w, h, w, h, fontStack)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, w, h, t.Bg)

	// Kicker.
	kicker := fmt.Sprintf("%s LINES · %s COMMITS · %s–%s",
		fmtK(s.Totals.Lines), fmtComma(s.Totals.Commits), s.FirstYear, s.Eras.After.To)
	fmt.Fprintf(&b, `<text x="32" y="44" font-size="13" letter-spacing="1.5" fill="%s">%s</text>`, t.Muted, esc(strings.ToUpper(kicker)))

	// Headline (wrapped to 2 lines).
	for i, ln := range wrap(Card1Headline(s), 46) {
		if i > 1 {
			break
		}
		fmt.Fprintf(&b, `<text x="32" y="%d" font-size="24" font-weight="700" fill="%s">%s</text>`, 80+i*30, t.Fg, esc(ln))
	}

	// Two panels.
	panelY, panelH := 150, 190
	panel(&b, 32, panelY, 360, panelH, t.Panel, t.Fg, t.Muted, "BEFORE AGENTS", s.Eras.Before, s, false, t)
	panel(&b, 408, panelY, 360, panelH, t.PanelHi, t.OnHi, "#94a3b8", "AFTER AGENTS", s.Eras.After, s, true, t)

	// Callout.
	callout := fmt.Sprintf("The pace changed, not the hours. %s shipped %s lines — more than any full year before agents (best: %s in %s).",
		monthName(s.PeakMonth.Month), fmtK(s.PeakMonth.Lines), fmtK(bestBeforeLines(s)), s.PeakMonth.BeatsBestYear)
	for i, ln := range wrap(callout, 96) {
		if i > 1 {
			break
		}
		fmt.Fprintf(&b, `<text x="32" y="%d" font-size="13" fill="%s">%s</text>`, 366+i*18, t.Muted, esc(ln))
	}

	fmt.Fprint(&b, footer(32, h-14, t, fmt.Sprintf("excluded: %s lines of imports & generated code", fmtK(s.Excluded.Lines))))
	fmt.Fprint(&b, `</svg>`)
	return b.String()
}

func bestBeforeLines(s *analyzer.Stats) int {
	best := 0
	for y, ys := range s.Years {
		if y < s.AgentStart[:4] && ys.Lines > best {
			best = ys.Lines
		}
	}
	return best
}

// panel draws one before/after column.
func panel(b *strings.Builder, x, y, w, h int, bg, fg, muted, title string, e analyzer.Era, s *analyzer.Stats, after bool, t Theme) {
	fmt.Fprintf(b, `<rect x="%d" y="%d" width="%d" height="%d" rx="10" fill="%s"/>`, x, y, w, h, bg)
	px := x + 20
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="12" letter-spacing="1.5" fill="%s">%s</text>`, px, y+28, muted, title)
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="12" fill="%s">%s</text>`, px, y+48, muted, e.Label())
	// Big lines number.
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="46" font-weight="800" fill="%s">%s</text>`, px, y+96, fg, fmtK(e.Lines))
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="13" fill="%s">lines of code</text>`, px, y+114, muted)

	top := topLangName(e.Langs)
	topPct := 0.0
	if e.Lines > 0 && top != "" {
		topPct = float64(e.Langs[top]) / float64(e.Lines) * 100
	}
	rows := []string{
		fmt.Sprintf("top language: %s (%.0f%%)", top, topPct),
		fmt.Sprintf("commits: %s", fmtComma(e.Commits)),
	}
	if after {
		rows = append(rows, fmt.Sprintf("this year so far: %s lines", fmtK(e.PaceThisYear)))
		rows = append(rows, fmt.Sprintf("%d languages in daily use", e.DailyLangs))
	} else {
		rows = append(rows, fmt.Sprintf("pace: %s lines/year", fmtK(e.PacePerYear)))
		rows = append(rows, fmt.Sprintf("%d languages cover most of it", e.DailyLangs))
	}
	for i, r := range rows {
		fmt.Fprintf(b, `<text x="%d" y="%d" font-size="14" fill="%s">%s</text>`, px, y+142+i*22, fg, esc(r))
	}
}

// Composition renders card 2 (800×520).
func Composition(s *analyzer.Stats, t Theme) string {
	const w, h = 800, 520
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="%s">`, w, h, w, h, fontStack)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, w, h, t.Bg)

	fmt.Fprintf(&b, `<text x="32" y="40" font-size="13" letter-spacing="1.5" fill="%s">WHAT THE CODE IS MADE OF</text>`, t.Muted)
	fmt.Fprintf(&b, `<text x="32" y="76" font-size="24" font-weight="700" fill="%s">%s</text>`, t.Fg, esc(Card2Headline(s)))

	// Two 100% stacked bars.
	stackedBar(&b, 32, 104, 736, 34, topLangs(s.Eras.Before.Langs, 6), t, fmt.Sprintf("BEFORE — %s", eraLabel(s.Eras.Before.Langs)))
	stackedBar(&b, 32, 176, 736, 34, topLangs(s.Eras.After.Langs, 6), t, fmt.Sprintf("AFTER — %s", eraLabel(s.Eras.After.Langs)))

	// Lines-per-month chart since agent start.
	monthChart(&b, 32, 268, 736, 130, s, t)

	// Three before→after stat pairs.
	pairY := 430
	statPair(&b, 32, pairY, "lines per commit", s.Eras.Before.LinesPerCommit, s.Eras.After.LinesPerCommit, t, false)
	statPair(&b, 288, pairY, "repos touched", s.Eras.Before.ReposTouched, s.Eras.After.ReposTouched, t, false)
	statPair(&b, 544, pairY, "languages in daily use", s.Eras.Before.DailyLangs, s.Eras.After.DailyLangs, t, false)

	fmt.Fprint(&b, footer(32, h-14, t, ""))
	fmt.Fprint(&b, `</svg>`)
	return b.String()
}

// Card2Headline builds the language-shift headline.
func Card2Headline(s *analyzer.Stats) string {
	beforeTop := topLangs(s.Eras.Before.Langs, 6)
	afterTop := topLangs(s.Eras.After.Langs, 6)
	if len(beforeTop) == 0 || len(afterTop) == 0 {
		return "How the mix of languages changed."
	}
	bl := beforeTop[0]
	// after pct of the same language
	afterPct := 0.0
	if s.Eras.After.Lines > 0 {
		afterPct = float64(s.Eras.After.Langs[bl.Lang]) / float64(s.Eras.After.Lines) * 100
	}
	inAfterTop := false
	for _, l := range afterTop {
		if l.Lang == bl.Lang {
			inAfterTop = true
		}
	}
	if !inAfterTop && afterPct < 5 {
		return fmt.Sprintf("%s was %.0f%% of my work. Now %s is %.0f%%.", bl.Lang, bl.Pct, afterTop[0].Lang, afterTop[0].Pct)
	}
	return fmt.Sprintf("%s was %.0f%% of my work. Now it is %.0f%%.", bl.Lang, bl.Pct, afterPct)
}

func stackedBar(b *strings.Builder, x, y, w, barH int, shares []langShare, t Theme, label string) {
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="12" letter-spacing="1" fill="%s">%s</text>`, x, y-6, t.Muted, esc(strings.ToUpper(label)))
	cx := x
	for _, sh := range shares {
		sw := int(float64(w) * sh.Pct / 100)
		if sw < 1 {
			continue
		}
		fmt.Fprintf(b, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`, cx, y, sw, barH, langColor(sh.Lang))
		if sw > 46 {
			fmt.Fprintf(b, `<text x="%d" y="%d" font-size="11" fill="#ffffff">%s %.0f%%</text>`, cx+6, y+barH/2+4, esc(sh.Lang), sh.Pct)
		}
		cx += sw
	}
}

func monthChart(b *strings.Builder, x, y, w, hgt int, s *analyzer.Stats, t Theme) {
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="12" letter-spacing="1" fill="%s">LINES PER MONTH SINCE %s</text>`, x, y-6, t.Muted, s.AgentStart)
	// months from agent start, chronological.
	var months []string
	for _, m := range sortedMonths(s.Months) {
		if m >= s.AgentStart {
			months = append(months, m)
		}
	}
	if len(months) == 0 {
		return
	}
	maxV := 1
	for _, m := range months {
		if s.Months[m] > maxV {
			maxV = s.Months[m]
		}
	}
	gap := 3
	bw := (w - gap*(len(months)-1)) / len(months)
	if bw < 1 {
		bw = 1
	}
	for i, m := range months {
		bh := int(float64(hgt) * float64(s.Months[m]) / float64(maxV))
		bx := x + i*(bw+gap)
		fill := t.Grid
		if m == s.PeakMonth.Month {
			fill = t.Accent
		}
		fmt.Fprintf(b, `<rect x="%d" y="%d" width="%d" height="%d" rx="2" fill="%s"/>`, bx, y+hgt-bh, bw, bh, fill)
	}
	cap := fmt.Sprintf("The red bar is %s: %s lines in one month.", monthName(s.PeakMonth.Month), fmtK(s.PeakMonth.Lines))
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="12" fill="%s">%s</text>`, x, y+hgt+18, t.Accent, esc(cap))
}

func statPair(b *strings.Builder, x, y int, label string, before, after int, t Theme, _ bool) {
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="12" fill="%s">%s</text>`, x, y, t.Muted, esc(label))
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="22" font-weight="700" fill="%s">%s → %s</text>`,
		x, y+26, t.Fg, fmtNum(before), fmtNum(after))
}

func fmtNum(n int) string {
	if n >= 1000 {
		return fmtK(n)
	}
	return fmtComma(n)
}

// Badge renders the shields-style README badge.
func Badge(s *analyzer.Stats, t Theme) string {
	mult := 0.0
	if s.Eras.Before.PacePerYear > 0 {
		mult = float64(s.Eras.After.PaceThisYear) / float64(s.Eras.Before.PacePerYear)
	}
	right := fmt.Sprintf("%s lines · %.1f×", fmtK(s.Eras.After.Lines), mult)
	left := "agent era"
	lw := 8 + len(left)*7
	rw := 12 + len(right)*7
	w := lw + rw
	h := 20
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="%s">`, w, h, w, h, "Verdana,Geneva,DejaVu Sans,sans-serif")
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="3" fill="#555"/>`, w, h)
	fmt.Fprintf(&b, `<rect x="%d" width="%d" height="%d" rx="3" fill="%s"/>`, lw, rw, h, t.Accent)
	fmt.Fprintf(&b, `<rect x="%d" width="6" height="%d" fill="%s"/>`, lw, h, t.Accent) // square off the left of the accent
	fmt.Fprintf(&b, `<text x="%d" y="14" fill="#fff" font-size="11">%s</text>`, 6, esc(left))
	fmt.Fprintf(&b, `<text x="%d" y="14" fill="#fff" font-size="11">%s</text>`, lw+6, esc(right))
	fmt.Fprint(&b, `</svg>`)
	return b.String()
}

// OG renders the 1200×630 social-share image (og.png). It reuses the card-1
// headline and the before/after numbers in a layout sized for LinkedIn / X
// (twitter:card summary_large_image) previews. Rasterised to PNG via resvg.
func OG(s *analyzer.Stats, t Theme) string {
	const w, h = 1200, 630
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="%s">`, w, h, w, h, fontStack)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, w, h, t.Bg)
	// Left accent rule.
	fmt.Fprintf(&b, `<rect x="0" y="0" width="14" height="%d" fill="%s"/>`, h, t.Accent)

	// Kicker.
	kicker := fmt.Sprintf("%s · %s LINES · %s COMMITS · %s–%s",
		esc(s.Login), fmtK(s.Totals.Lines), fmtComma(s.Totals.Commits), s.FirstYear, s.Eras.After.To)
	fmt.Fprintf(&b, `<text x="72" y="96" font-size="24" letter-spacing="1.5" fill="%s">%s</text>`, t.Muted, strings.ToUpper(kicker))

	// Headline (up to 3 lines, large).
	for i, ln := range wrap(Card1Headline(s), 34) {
		if i > 2 {
			break
		}
		fmt.Fprintf(&b, `<text x="72" y="%d" font-size="54" font-weight="800" fill="%s">%s</text>`, 190+i*66, t.Fg, esc(ln))
	}

	// Two big era numbers.
	ogEra(&b, 72, 420, "BEFORE AGENTS", s.Eras.Before, t, false)
	ogEra(&b, 640, 420, "AFTER AGENTS", s.Eras.After, t, true)

	fmt.Fprint(&b, footer(72, h-40, t, "agentstats.deemwar.com"))
	fmt.Fprint(&b, `</svg>`)
	return b.String()
}

func ogEra(b *strings.Builder, x, y int, title string, e analyzer.Era, t Theme, after bool) {
	col := t.Fg
	if after {
		col = t.Accent
	}
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="22" letter-spacing="1.5" fill="%s">%s (%s)</text>`, x, y, t.Muted, title, e.Label())
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="72" font-weight="800" fill="%s">%s</text>`, x, y+72, col, fmtK(e.Lines))
	fmt.Fprintf(b, `<text x="%d" y="%d" font-size="22" fill="%s">lines · top: %s</text>`, x, y+108, t.Muted, esc(topLangName(e.Langs)))
}

// BadgeStyle controls the shield shape (basic set for the badge builder).
type BadgeStyle string

const (
	StyleFlat        BadgeStyle = "flat"
	StyleFlatSquare  BadgeStyle = "flat-square"
	StyleForTheBadge BadgeStyle = "for-the-badge"
)

// ShieldBadge renders a shields-style two-segment badge with a chosen label,
// value, theme and style. accent overrides the right-segment colour when set.
// This backs the badge builder (doc 12) — metric/style/theme + copy snippets.
func ShieldBadge(label, value string, t Theme, style BadgeStyle, accent string) string {
	right := value
	left := label
	upper := false
	charW := 7
	h := 20
	pad := 6
	rx := 3
	if style == StyleForTheBadge {
		upper = true
		charW = 9
		h = 28
		pad = 12
		rx = 0
	} else if style == StyleFlatSquare {
		rx = 0
	}
	lt, rt := left, right
	if upper {
		lt, rt = strings.ToUpper(left), strings.ToUpper(right)
	}
	lw := pad*2 + len(lt)*charW
	rw := pad*2 + len(rt)*charW
	w := lw + rw
	accentCol := t.Accent
	if accent != "" {
		accentCol = accent
	}
	fontSize := 11
	if upper {
		fontSize = 12
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="Verdana,Geneva,DejaVu Sans,sans-serif">`, w, h, w, h)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" rx="%d" fill="#555"/>`, w, h, rx)
	fmt.Fprintf(&b, `<rect x="%d" width="%d" height="%d" rx="%d" fill="%s"/>`, lw, rw, h, rx, accentCol)
	if rx > 0 {
		fmt.Fprintf(&b, `<rect x="%d" width="%d" height="%d" fill="%s"/>`, lw, rx, h, accentCol) // square the join
	}
	ty := h/2 + fontSize/2 - 1
	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#fff" font-size="%d">%s</text>`, pad, ty, fontSize, esc(lt))
	fmt.Fprintf(&b, `<text x="%d" y="%d" fill="#fff" font-size="%d">%s</text>`, lw+pad, ty, fontSize, esc(rt))
	fmt.Fprint(&b, `</svg>`)
	return b.String()
}

// wrap breaks text into lines of at most n characters on word boundaries.
func wrap(text string, n int) []string {
	words := strings.Fields(text)
	var lines []string
	cur := ""
	for _, wd := range words {
		if cur == "" {
			cur = wd
		} else if len(cur)+1+len(wd) <= n {
			cur += " " + wd
		} else {
			lines = append(lines, cur)
			cur = wd
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}
