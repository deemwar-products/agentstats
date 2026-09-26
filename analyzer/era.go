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

// DetectEra picks the AI release after which the user's monthly output rose the most.
// It uses nothing but their own counted lines per month and the release timeline, so it
// works whether or not the agent signed any commits.
func DetectEra(monthly map[string]int, ms []Milestone) EraPick {
	last := ""
	for m := range monthly {
		if m > last {
			last = m
		}
	}
	best := EraPick{Month: FallbackEra, Auto: true}
	bestRatio := 0.0
	seen := map[string]bool{}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Date < ms[j].Date })
	for _, m := range ms {
		if len(m.Date) < 7 || m.Date < "2021-06" {
			continue
		}
		start := m.Date[:7]
		if seen[start] || start > last {
			continue
		}
		seen[start] = true
		after, n := 0, 0
		for i := 0; i < eraAfterMonths; i++ {
			mm := addMonths(start, i)
			if mm > last {
				break
			}
			after += monthly[mm]
			n++
		}
		if n < eraMinAfter {
			continue
		}
		before := 0
		for i := 1; i <= eraBeforeMonths; i++ {
			before += monthly[addMonths(start, -i)]
		}
		afterPace := float64(after) / float64(n)
		beforePace := max(float64(before)/eraBeforeMonths, eraPaceFloor)
		ratio := afterPace / beforePace
		if ratio > bestRatio {
			bestRatio = ratio
			best = EraPick{Month: start, Milestone: m.Tool, Ratio: round1(ratio), Auto: true}
		}
	}
	if bestRatio < eraMinRatio {
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
