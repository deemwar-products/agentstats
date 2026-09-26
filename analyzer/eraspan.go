package analyzer

import (
	"fmt"
	"strconv"
)

var monthAbbr = [...]string{"", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// NormalizeEras makes the before/after spans follow the month the agent era
// starts in, not the calendar year: an era detected as Nov 2025 reads
// "2013 – Oct 2025" / "Nov 2025 – Sep 2026", and the before pace divides by
// the months actually covered. Idempotent; safe on stored stats.
func (s *Stats) NormalizeEras() {
	if s == nil || len(s.AgentStart) < 7 || len(s.Months) == 0 {
		return
	}
	first, last := "", ""
	for m := range s.Months {
		if first == "" || m < first {
			first = m
		}
		if m > last {
			last = m
		}
	}
	if last < s.AgentStart {
		last = s.AgentStart
	}
	beforeEnd := addMonths(s.AgentStart, -1)
	b, a := &s.Eras.Before, &s.Eras.After
	if first != "" && first <= beforeEnd {
		b.Months = monthDiff(first, beforeEnd) + 1
		b.Span = spanTxt(first, beforeEnd, true)
		if b.Months > 0 {
			b.PacePerYear = int(float64(b.Lines)*12/float64(b.Months) + 0.5)
		}
	}
	a.Months = monthDiff(s.AgentStart, last) + 1
	a.Span = spanTxt(s.AgentStart, last, false)
}

// Label is the era's display span, month-aware once NormalizeEras has run.
func (e Era) Label() string {
	if e.Span != "" {
		return e.Span
	}
	return e.From + "–" + e.To
}

// YearCount is the era length in whole years (rounded, at least 1 when non-empty).
func (e Era) YearCount() int {
	if e.Months > 0 {
		if y := (e.Months + 6) / 12; y > 0 {
			return y
		}
		return 1
	}
	f, _ := strconv.Atoi(e.From)
	t, _ := strconv.Atoi(e.To)
	if f == 0 || t < f {
		return 0
	}
	return t - f + 1
}

func spanTxt(from, to string, isBefore bool) string {
	fy, fm := ymParts(from)
	ty, tm := ymParts(to)
	whole := fm == 1 && tm == 12
	if isBefore {
		whole = tm == 12 // the before era's start is just "when you began"
	}
	if whole {
		if fy == ty {
			return strconv.Itoa(fy)
		}
		return fmt.Sprintf("%d–%d", fy, ty)
	}
	if isBefore {
		return fmt.Sprintf("%d – %s %d", fy, monthAbbr[tm], ty)
	}
	return fmt.Sprintf("%s %d – %s %d", monthAbbr[fm], fy, monthAbbr[tm], ty)
}

func ymParts(ym string) (int, int) {
	if len(ym) < 7 {
		return 0, 0
	}
	y, _ := strconv.Atoi(ym[:4])
	m, _ := strconv.Atoi(ym[5:7])
	if m < 1 || m > 12 {
		m = 1
	}
	return y, m
}

func monthDiff(from, to string) int {
	fy, fm := ymParts(from)
	ty, tm := ymParts(to)
	return (ty*12 + tm) - (fy*12 + fm)
}
