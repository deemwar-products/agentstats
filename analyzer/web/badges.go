package web

import (
	"fmt"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/render"
)

// Badge builder — BASIC set for the test deploy (doc 12 "whatever they want",
// trimmed per owner priority): metric + style + theme, live preview, copy
// snippets. The full custom label/accent/period matrix is a later refinement.

type optItem struct {
	Value string
	Label string
}

var badgeMetrics = []optItem{
	{"agent-lines", "Agent-era lines"},
	{"multiplier", "Pace multiplier"},
	{"peak-month", "Peak month"},
	{"agent-signed", "Agent-signed %"},
	{"top-tool", "Top agent tool"},
	{"top-language", "Top language now"},
	{"total-lines", "Total honest lines"},
	{"commits-after", "Commits since agents"},
}

var badgeStyles = []optItem{
	{"flat", "flat"},
	{"flat-square", "flat-square"},
	{"for-the-badge", "for-the-badge"},
}

var badgeThemes = []optItem{
	{"light", "light"},
	{"dark", "dark"},
	{"deemwar", "deemwar"},
}

func themeByName(name string) render.Theme {
	if name == "dark" || name == "deemwar" {
		return render.Dark
	}
	return render.Light
}

func styleByName(name string) render.BadgeStyle {
	switch name {
	case "flat-square":
		return render.StyleFlatSquare
	case "for-the-badge":
		return render.StyleForTheBadge
	default:
		return render.StyleFlat
	}
}

// metricValue returns the (label, value) for a badge metric computed from Stats.
func metricValue(metric string, s *analyzer.Stats) (string, string) {
	switch metric {
	case "multiplier":
		mult := 0.0
		if s.Eras.Before.PacePerYear > 0 {
			mult = float64(s.Eras.After.PaceThisYear) / float64(s.Eras.Before.PacePerYear)
		}
		return "agent pace", fmt.Sprintf("%.1fx", mult)
	case "peak-month":
		return "peak month", fmt.Sprintf("%s lines", render.FmtK(s.PeakMonth.Lines))
	case "agent-signed":
		pct := 0.0
		if s.Agents != nil {
			pct = s.Agents.ShareOfAfterEra * 100
		}
		return "agent-signed", fmt.Sprintf(">=%.0f%%", pct)
	case "top-tool":
		return "top tool", topTool(s)
	case "top-language":
		return "top language", render.TopLangName(s.Eras.After.Langs)
	case "total-lines":
		return "honest lines", render.FmtK(s.Totals.Lines)
	case "commits-after":
		return "agent-era commits", fmt.Sprintf("%d", s.Eras.After.Commits)
	default: // agent-lines
		return "agent era", fmt.Sprintf("%s lines", render.FmtK(s.Eras.After.Lines))
	}
}

func topTool(s *analyzer.Stats) string {
	if s.Agents == nil {
		return "n/a"
	}
	best, bestC := "n/a", -1
	for tool, ts := range s.Agents.ByTool {
		if ts.Commits > bestC {
			best, bestC = tool, ts.Commits
		}
	}
	return best
}

// renderBadge builds the SVG for a badge config against a user's stats.
func renderBadge(metric, style, theme, label, accent string, s *analyzer.Stats) string {
	l, v := metricValue(metric, s)
	if label != "" {
		l = label
	}
	if accent == "" { // value colours from the approved mockup
		switch theme {
		case "deemwar":
			accent = "#c8553d"
		case "dark":
			accent = "#b8861b"
		default:
			accent = "#3f7d5c"
		}
	}
	return render.ShieldBadge(l, v, themeByName(theme), styleByName(style), accent)
}
