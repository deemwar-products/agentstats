package analyzer

import (
	"sort"
	"strconv"
)

// AutoEra asks the analyzer to detect the agent-era start from the user's own history.
const AutoEra = "auto"

// FallbackEra is used when no AI release visibly changed someone's pace.
const FallbackEra = "2025-01"

// EraPick explains where the before/after boundary came from.
type EraPick struct {
	Month     string  `json:"month"`               // YYYY-MM boundary (first "after" month)
	Milestone string  `json:"milestone,omitempty"` // the AI release it lines up with
	Ratio     float64 `json:"ratio,omitempty"`     // after-pace ÷ before-pace around that release
	Auto      bool    `json:"auto"`                // detected, not chosen by the user
}

// Detection windows: pace in the 6 months from a release vs the 24 months before it.
const (
	eraAfterMonths  = 6
	eraBeforeMonths = 24
	eraMinAfter     = 3    // need at least this many months of data after a release
	eraPaceFloor    = 1000 // lines/month floor for the "before" pace, so a quiet past can't make any bump look huge
	eraMinRatio     = 1.5  // a release must lift the pace at least this much to count
)

// ReleaseImpact is one release month scored the way DetectEra scores it: average monthly
// lines in the eraAfterMonths from that month vs the eraBeforeMonths before it.
type ReleaseImpact struct {
	Month      string   // YYYY-MM
	Tools      []string // every release that month, in timeline order
	AfterPace  float64
	BeforePace float64 // floored at eraPaceFloor
	Ratio      float64 // rounded to 0.1
	Eligible   bool    // enough months after it to judge
}

// ReleaseImpacts scores every release month inside the user's history. The dashboard's
// release panel and DetectEra both use it, so the two always agree.
func ReleaseImpacts(monthly map[string]int, ms []Milestone) []ReleaseImpact {
	last := ""
	for m := range monthly {
		if m > last {
			last = m
		}
	}
	sorted := append([]Milestone(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })
	var out []ReleaseImpact
	for _, m := range sorted {
		if len(m.Date) < 7 || m.Date < "2021-06" {
			continue
		}
		start := m.Date[:7]
		if start > last {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Month == start {
			out[n-1].Tools = append(out[n-1].Tools, m.Tool)
			continue
		}
		after, n := 0, 0
		for i := 0; i < eraAfterMonths; i++ {
			mm := addMonths(start, i)
			if mm > last {
				break
			}
			after += monthly[mm]
			n++
		}
		before := 0
		for i := 1; i <= eraBeforeMonths; i++ {
			before += monthly[addMonths(start, -i)]
		}
		ri := ReleaseImpact{Month: start, Tools: []string{m.Tool}, Eligible: n >= eraMinAfter,
			BeforePace: max(float64(before)/eraBeforeMonths, eraPaceFloor)}
		if n > 0 {
			ri.AfterPace = float64(after) / float64(n)
			ri.Ratio = round1(ri.AfterPace / ri.BeforePace)
		}
		out = append(out, ri)
	}
	return out
}

// DetectEra picks the AI release after which the user's monthly output rose the most.
// It uses nothing but their own counted lines per month and the release timeline, so it
// works whether or not the agent signed any commits.
func DetectEra(monthly map[string]int, ms []Milestone) EraPick {
	best := EraPick{Month: FallbackEra, Auto: true}
	for _, ri := range ReleaseImpacts(monthly, ms) {
		if ri.Eligible && ri.Ratio > best.Ratio {
			best = EraPick{Month: ri.Month, Milestone: ri.Tools[0], Ratio: ri.Ratio, Auto: true}
		}
	}
	if best.Ratio < eraMinRatio {
		return EraPick{Month: FallbackEra, Auto: true}
	}
	return best
}

// addMonths shifts a YYYY-MM string by n months.
func addMonths(ym string, n int) string {
	y, _ := strconv.Atoi(ym[:4])
	m, _ := strconv.Atoi(ym[5:7])
	t := y*12 + (m - 1) + n
	return pad4(t/12) + "-" + pad2(t%12+1)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func pad4(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
