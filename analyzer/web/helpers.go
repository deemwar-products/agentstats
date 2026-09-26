package web

import (
	crand "crypto/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/deemwar-products/agentstats/analyzer"
)

func cryptoRead(b []byte) (int, error) { return crand.Read(b) }

func getenv(k string) string { return os.Getenv(k) }

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// sampleStats is the illustrative dataset behind the landing-page sample cards
// and badge previews. Numbers are clearly a sample (login "sample-dev"), not
// anyone's real history, and no private repo names appear.
func sampleStats() *analyzer.Stats {
	return &analyzer.Stats{
		Login:      "sample-dev",
		AgentStart: "2025-01",
		FirstYear:  "2013",
		Totals:     analyzer.Totals{Lines: 1638308, Commits: 5150, Repos: 326},
		Eras: analyzer.Eras{
			Before: analyzer.Era{
				From: "2013", To: "2024", Lines: 386569, Commits: 1355, ReposTouched: 142,
				LinesPerCommit: 285, DailyLangs: 12, PacePerYear: 32214,
				Langs: map[string]int{"JavaScript": 134018, "Java": 55951, "HTML": 43266, "CSS": 29862, "Groovy": 29630, "Python": 18456, "Clojure": 13393, "TypeScript": 10701},
			},
			After: analyzer.Era{
				From: "2025", To: "2026", Lines: 1251739, Commits: 3795, ReposTouched: 93,
				LinesPerCommit: 330, DailyLangs: 10, PaceThisYear: 1128412,
				Langs: map[string]int{"Go": 573260, "TypeScript": 142068, "Python": 124824, "Java": 104801, "JavaScript": 82368, "HTML": 51230, "Elixir": 44023, "Shell": 37437},
			},
		},
		Years:     map[string]analyzer.YearStat{"2013": {Lines: 10441, Commits: 22}, "2014": {Lines: 57534, Commits: 142}, "2015": {Lines: 23525, Commits: 80}, "2016": {Lines: 35790, Commits: 52}, "2017": {Lines: 11332, Commits: 40}, "2018": {Lines: 740, Commits: 6}, "2019": {Lines: 43668, Commits: 109}, "2020": {Lines: 20180, Commits: 146}, "2021": {Lines: 42060, Commits: 195}, "2022": {Lines: 35222, Commits: 158}, "2023": {Lines: 11423, Commits: 83}, "2024": {Lines: 94654, Commits: 322}, "2025": {Lines: 123327, Commits: 266}, "2026": {Lines: 1128412, Commits: 3529}},
		Months:    map[string]int{"2025-01": 127, "2025-02": 4090, "2025-03": 438, "2025-04": 975, "2025-05": 12710, "2025-06": 15383, "2025-07": 6907, "2025-08": 13861, "2025-09": 20265, "2025-10": 3895, "2025-11": 44669, "2025-12": 7, "2026-01": 747, "2026-02": 1524, "2026-03": 12511, "2026-04": 125599, "2026-05": 53378, "2026-06": 58573, "2026-07": 567485, "2026-08": 203105, "2026-09": 105490},
		PeakMonth: analyzer.PeakMonth{Month: "2026-07", Lines: 567485, BeatsBestYear: "2024"},
		Excluded: analyzer.Excluded{
			Commits: 25, Lines: 4212006,
			Top: []analyzer.DroppedSummary{
				{Repo: "a private repo", SHA: "ecb85df8", Files: 16239, Lines: 3259905, Reason: "bulk-import"},
			},
		},
		SkippedRepos: []analyzer.SkippedRepo{},
	}
}
