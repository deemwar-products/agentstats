package analyzer

import "regexp"

// Counting rules — the single source of truth, mirrored by the site's
// methodology page. Ported verbatim from prototype/stats.py so the Go
// analyzer reproduces the reference numbers exactly.

// Per-file and per-commit thresholds.
const (
	// MaxFileAdded: skip a single file that adds more than this many lines
	// (pasted blobs / vendored dumps that slipped the path filter).
	MaxFileAdded = 5000
	// MaxCommitFiles / MaxCommitLines: drop a whole commit that touches more
	// files or adds more surviving code lines than this (bulk imports).
	MaxCommitFiles = 300
	MaxCommitLines = 25000
)

// LANG maps a lowercase file extension to a language name. Extensions absent
// from this map are ignored entirely (treated as non-code).
var LANG = map[string]string{
	"rb": "Ruby", "erb": "Ruby", "rake": "Ruby",
	"js": "JavaScript", "jsx": "JavaScript", "mjs": "JavaScript", "cjs": "JavaScript",
	"ts": "TypeScript", "tsx": "TypeScript",
	"java": "Java", "kt": "Kotlin", "go": "Go", "py": "Python", "rs": "Rust",
	"swift": "Swift", "cs": "C#", "c": "C", "h": "C", "cpp": "C++", "cc": "C++",
	"php": "PHP", "dart": "Dart", "scala": "Scala", "groovy": "Groovy", "gradle": "Groovy",
	"sh": "Shell", "bash": "Shell", "zsh": "Shell", "ps1": "PowerShell",
	"html": "HTML", "htm": "HTML", "css": "CSS", "scss": "CSS", "sass": "CSS", "less": "CSS",
	"vue": "Vue", "svelte": "Svelte", "sql": "SQL",
	"md": "Markdown", "mdx": "Markdown",
	"yml": "YAML", "yaml": "YAML",
	"lua": "Lua", "ex": "Elixir", "exs": "Elixir", "clj": "Clojure", "hs": "Haskell",
	"r": "R", "m": "Objective-C", "vim": "Vim", "tf": "Terraform", "proto": "Protobuf",
	"toml": "TOML", "json": "JSON", "xml": "XML", "ipynb": "Notebook",
}

// NOTCODE: languages that are in LANG for classification but are NOT counted as
// human-written code (config, docs, data, notebooks).
var NOTCODE = map[string]bool{
	"JSON": true, "XML": true, "Markdown": true, "YAML": true, "TOML": true, "Notebook": true,
}

// SKIP matches paths that never count: dependency/build/output dirs, lock
// files, minified/generated assets, data files, vendored code. Ported
// character-for-character from prototype/stats.py.
var SKIP = regexp.MustCompile(
	`(^|/)(node_modules|vendor|dist|build|target|\.next|coverage|bower_components|out|__pycache__|Pods|\.gradle|venv|\.venv)/` +
		`|(package-lock\.json|yarn\.lock|pnpm-lock\.yaml|go\.sum|Gemfile\.lock|Cargo\.lock|poetry\.lock|composer\.lock|bun\.lockb?)$` +
		`|\.min\.(js|css)$` +
		`|\.(map|snap|svg|lock|csv|tsv|txt|log|pb\.go)$` +
		`|_pb2\.py$` +
		`|\.g\.dart$` +
		`|generated` +
		`|(^|/)[a-z]*aot/` +
		`|vendor`,
)

// DailyLangThreshold: a language is "in daily use" for an era when it accounts
// for at least this share of the era's counted lines.
const DailyLangThreshold = 0.02
