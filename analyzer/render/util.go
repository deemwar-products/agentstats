package render

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"

	"github.com/deemwar-products/agentstats/analyzer"
)

// FmtK is the exported form of fmtK for callers outside the package (e.g. the
// web badge builder), so number formatting stays single-sourced here.
func FmtK(n int) string { return fmtK(n) }

// TopLangName is the exported form of topLangName.
func TopLangName(langs map[string]int) string { return topLangName(langs) }

// fmtK formats a line count as 647K / 1.25M; small numbers as-is.
func fmtK(n int) string {
	f := float64(n)
	switch {
	case n >= 1_000_000:
		return trimZero(fmt.Sprintf("%.2f", f/1e6)) + "M"
	case n >= 1000:
		return fmt.Sprintf("%.0fK", f/1000)
	default:
		return strconv.Itoa(n)
	}
}

func trimZero(s string) string {
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// fmtComma groups an integer with commas: 33501 -> "33,501".
func fmtComma(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + fmtComma(-n)
	}
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

func esc(s string) string { return html.EscapeString(s) }

// langShare is one language's slice of an era.
type langShare struct {
	Lang string
	Line int
	Pct  float64
}

// topLangs returns the top-n languages of a lang map plus an aggregated "Other"
// bucket, each with its percentage of the total.
func topLangs(langs map[string]int, n int) []langShare {
	total := 0
	list := make([]langShare, 0, len(langs))
	for l, v := range langs {
		total += v
		list = append(list, langShare{Lang: l, Line: v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Line != list[j].Line {
			return list[i].Line > list[j].Line
		}
		return list[i].Lang < list[j].Lang
	})
	if total == 0 {
		return nil
	}
	out := list
	if len(list) > n {
		other := 0
		for _, l := range list[n:] {
			other += l.Line
		}
		out = append([]langShare(nil), list[:n]...)
		if other > 0 {
			out = append(out, langShare{Lang: "Other", Line: other})
		}
	}
	for i := range out {
		out[i].Pct = float64(out[i].Line) / float64(total) * 100
	}
	return out
}

// eraLabel picks a short human label for an era from its top languages.
func eraLabel(langs map[string]int) string {
	top := topLangs(langs, 6)
	set := map[string]bool{}
	for _, l := range top {
		set[l.Lang] = true
	}
	switch {
	case set["Ruby"] && (set["JavaScript"] || set["HTML"]):
		return "a Ruby web stack"
	case set["Java"] && (set["JavaScript"] || set["TypeScript"]):
		return "enterprise Java + web"
	case set["Go"] && (set["Rust"] || set["Shell"] || set["TypeScript"]):
		return "systems, services and tooling"
	case set["Python"]:
		return "Python and scripting"
	case set["JavaScript"] || set["TypeScript"]:
		return "a JavaScript web stack"
	default:
		return "mixed"
	}
}

// sortedMonths returns the YYYY-MM keys of a month map in chronological order.
func sortedMonths(m map[string]int) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// monthsSince counts inclusive calendar months from start (YYYY-MM) to end.
func monthsSince(start, end string) int {
	sy, sm := ym(start)
	ey, em := ym(end)
	n := (ey*12 + em) - (sy*12 + sm) + 1
	if n < 1 {
		return 1
	}
	return n
}

func ym(s string) (int, int) {
	if len(s) < 7 {
		return 0, 0
	}
	y, _ := strconv.Atoi(s[:4])
	m, _ := strconv.Atoi(s[5:7])
	return y, m
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// topLangName returns the single biggest language of a map, or "".
func topLangName(langs map[string]int) string {
	best, bestV := "", -1
	for l, v := range langs {
		if v > bestV || (v == bestV && l < best) {
			best, bestV = l, v
		}
	}
	return best
}

func monthName(m string) string {
	names := []string{"", "January", "February", "March", "April", "May", "June",
		"July", "August", "September", "October", "November", "December"}
	_, mm := ym(m)
	if mm >= 1 && mm <= 12 {
		return names[mm] + " " + m[:4]
	}
	return m
}

// lastMonth returns the latest YYYY-MM with data (falls back to peak or year).
func lastMonth(s *analyzer.Stats) string {
	ms := sortedMonths(s.Months)
	if len(ms) > 0 {
		return ms[len(ms)-1]
	}
	return s.PeakMonth.Month
}
