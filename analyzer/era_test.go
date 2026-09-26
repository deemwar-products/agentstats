package analyzer

import "testing"

func TestDetectEraPicksTheReleaseThatBentTheCurve(t *testing.T) {
	ms := []Milestone{
		{Date: "2022-11-30", Tool: "ChatGPT"},
		{Date: "2025-02-24", Tool: "Claude Code"},
		{Date: "2025-11-24", Tool: "Claude Opus 4.5"},
	}
	monthly := map[string]int{}
	for m := "2021-01"; m <= "2026-09"; m = addMonths(m, 1) {
		monthly[m] = 3000 // steady pace for years
	}
	for m := "2025-11"; m <= "2026-09"; m = addMonths(m, 1) {
		monthly[m] = 30000 // 10x after Opus 4.5
	}
	got := DetectEra(monthly, ms)
	if got.Month != "2025-11" || got.Milestone != "Claude Opus 4.5" || !got.Auto || got.Ratio < 5 {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectEraFallsBackWhenNothingChanged(t *testing.T) {
	monthly := map[string]int{}
	for m := "2020-01"; m <= "2026-09"; m = addMonths(m, 1) {
		monthly[m] = 5000
	}
	got := DetectEra(monthly, []Milestone{{Date: "2025-02-24", Tool: "Claude Code"}})
	if got.Month != FallbackEra || got.Milestone != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestAddMonths(t *testing.T) {
	for _, c := range [][3]string{{"2025-01", "-1", "2024-12"}, {"2025-11", "3", "2026-02"}, {"2025-06", "0", "2025-06"}} {
		n := map[string]int{"-1": -1, "3": 3, "0": 0}[c[1]]
		if got := addMonths(c[0], n); got != c[2] {
			t.Errorf("addMonths(%s,%d)=%s want %s", c[0], n, got, c[2])
		}
	}
}
