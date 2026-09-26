package web

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/render"
	"github.com/deemwar-products/agentstats/analyzer/store"
)

// seedDevBoard fills the store with N obviously fake developers so the
// leaderboard can be looked at locally. Dev-only: called from New when
// DEV_AUTH=1 and AGENTSTATS_DEV_SEED=<n> are both set. Handles are made up
// ("ada-builds7"); no real person's numbers appear.
func (s *Server) seedDevBoard(nStr string) {
	n, _ := strconv.Atoi(nStr)
	if n <= 0 {
		return
	}
	first := []string{"ada", "byte", "kline", "rivera", "nova", "juno", "pixel", "quill", "remy", "sage", "tariq", "uma", "vik", "wren", "yara", "zed"}
	last := []string{"builds", "codes", "dev", "hacks", "ships", "labs", "ops", "io", "works", "stack"}
	rng := rand.New(rand.NewSource(42))
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < n; i++ {
		login := fmt.Sprintf("%s-%s%d", first[i%len(first)], last[(i*7)%len(last)], i)
		st := fakeStats(login, rng, i%9 == 8) // every 9th has too little history for "shift"
		id := devID(login)
		_ = s.store.UpsertUser(store.User{GithubID: id, Login: login, Name: login, AgentStart: "auto", IsPublic: i%13 != 12, CreatedAt: now})
		if i%11 == 10 { // an opted-out user: must not appear
			_ = s.store.UpdateSettings(id, "auto", true, nil, false, false)
		}
		sj, _ := json.Marshal(st)
		_ = s.store.SaveStats(store.StatsRow{GithubID: id, Version: 1, ComputedAt: now, StatsJSON: string(sj), Headline: render.Card1Headline(st)})
	}
}

func fakeStats(login string, rng *rand.Rand, short bool) *analyzer.Stats {
	fromYear := 2012 + rng.Intn(9)
	if short {
		fromYear = 2024
	}
	beforeYears := 2025 - fromYear
	perYear := 3000 + rng.Intn(50000)
	before := perYear * beforeYears
	if short {
		before = 1500 + rng.Intn(2500)
	}
	mult := 0.8 + rng.Float64()*rng.Float64()*32
	afterMonths := 21 // 2025-01 .. 2026-09
	after := int(float64(before) / float64(beforeYears*12) * mult * float64(afterMonths))
	months := map[string]int{}
	peak, peakM := 0, ""
	weights, total := make([]float64, afterMonths), 0.0
	for i := range weights {
		weights[i] = 0.2 + rng.Float64()*float64(i+1)
		total += weights[i]
	}
	for i := 0; i < afterMonths; i++ {
		m := fmt.Sprintf("%d-%02d", 2025+i/12, i%12+1)
		v := int(float64(after) * weights[i] / total)
		months[m] = v
		if v > peak {
			peak, peakM = v, m
		}
	}
	return &analyzer.Stats{
		Login: login, AgentStart: "2025-01", FirstYear: strconv.Itoa(fromYear),
		Totals: analyzer.Totals{Lines: before + after},
		Eras: analyzer.Eras{
			Before: analyzer.Era{From: strconv.Itoa(fromYear), To: "2024", Lines: before, PacePerYear: before / beforeYearsOr1(beforeYears)},
			After:  analyzer.Era{From: "2025", To: "2026", Lines: after},
		},
		Months:    months,
		PeakMonth: analyzer.PeakMonth{Month: peakM, Lines: peak},
	}
}

func beforeYearsOr1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
