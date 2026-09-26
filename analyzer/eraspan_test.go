package analyzer

import "testing"

func TestNormalizeErasFollowsTheDetectedMonth(t *testing.T) {
	s := &Stats{AgentStart: "2025-11", Months: map[string]int{"2013-03": 10, "2025-10": 10, "2026-09": 10}}
	s.Eras.Before.Lines = 1540
	s.NormalizeEras()
	b, a := s.Eras.Before, s.Eras.After
	if b.Label() != "2013 – Oct 2025" || a.Label() != "Nov 2025 – Sep 2026" {
		t.Fatalf("labels: %q / %q", b.Label(), a.Label())
	}
	if b.Months != 152 || a.Months != 11 || b.YearCount() != 13 {
		t.Fatalf("months %d/%d years %d", b.Months, a.Months, b.YearCount())
	}
	if b.PacePerYear != 122 { // 1540 lines over 152 months
		t.Fatalf("pace %d", b.PacePerYear)
	}
}

func TestNormalizeErasKeepsWholeYearsPlain(t *testing.T) {
	s := &Stats{AgentStart: "2025-01", Months: map[string]int{"2013-01": 1, "2026-09": 1}}
	s.NormalizeEras()
	if s.Eras.Before.Label() != "2013–2024" || s.Eras.After.Label() != "Jan 2025 – Sep 2026" {
		t.Fatalf("%q / %q", s.Eras.Before.Label(), s.Eras.After.Label())
	}
}
