// Package render turns the analyzer's aggregate Stats into the shareable card
// and badge SVGs. It runs in the Go container at the end of a job; the Worker
// never renders. PNGs are produced by piping the SVG through the bundled resvg
// binary (see png.go). Every artifact carries "agentstats by deemwar".
package render

// Theme is one colour scheme. Cards render in light and dark variants
// (?theme=dark selects the dark objects in R2).
type Theme struct {
	Name    string
	Bg      string
	Panel   string // "before" panel / secondary surface
	PanelHi string // "after" panel (accent-dark surface)
	Fg      string
	Muted   string
	Accent  string // peak / highlight
	Grid    string
	OnHi    string // text on the accent-dark surface
}

var Light = Theme{
	Name: "light", Bg: "#ffffff", Panel: "#f4f5f7", PanelHi: "#0f172a",
	Fg: "#0f172a", Muted: "#64748b", Accent: "#e11d48", Grid: "#e2e8f0", OnHi: "#f8fafc",
}

var Dark = Theme{
	Name: "dark", Bg: "#0b1120", Panel: "#111827", PanelHi: "#1e293b",
	Fg: "#f8fafc", Muted: "#94a3b8", Accent: "#fb7185", Grid: "#334155", OnHi: "#f8fafc",
}

// langColors gives each language a stable colour for the stacked bars.
var langColors = map[string]string{
	"Go": "#00add8", "TypeScript": "#3178c6", "JavaScript": "#f1e05a",
	"Python": "#3572a5", "Java": "#b07219", "Ruby": "#701516", "Rust": "#dea584",
	"C#": "#178600", "C": "#555555", "C++": "#f34b7d", "PHP": "#4f5d95",
	"HTML": "#e34c26", "CSS": "#563d7c", "Shell": "#89e051", "Clojure": "#db5855",
	"Elixir": "#6e4a7e", "Groovy": "#4298b8", "Dart": "#00b4ab", "Scala": "#c22d40",
	"SQL": "#e38c00", "Swift": "#f05138", "Kotlin": "#a97bff", "Vue": "#41b883",
	"Svelte": "#ff3e00", "PowerShell": "#012456", "Lua": "#000080",
	"Objective-C": "#438eff", "Protobuf": "#7fa1b3", "Terraform": "#844fba",
	"Haskell": "#5e5086", "R": "#198ce7", "Vim": "#199f4b", "Other": "#94a3b8",
}

func langColor(name string) string {
	if c, ok := langColors[name]; ok {
		return c
	}
	return "#94a3b8"
}
