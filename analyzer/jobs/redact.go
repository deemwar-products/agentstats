package jobs

import (
	"io"
	"regexp"
)

// tokenRe matches GitHub credentials: classic/OAuth/user-to-server/server/refresh
// tokens (ghp_ gho_ ghu_ ghs_ ghr_), fine-grained PATs (github_pat_) and any
// "Authorization: Bearer|Basic <x>" / "token <x>" header value that slips into output.
var tokenRe = regexp.MustCompile(`gh[opsur]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|(?i:(authorization:\s*(?:bearer|basic|token)\s+))[^\s"']+`)

// Redact replaces every GitHub token in s with a fixed marker. Use it on any
// string that may carry git/HTTP output before it reaches a log or an error.
func Redact(s string) string {
	return tokenRe.ReplaceAllStringFunc(s, func(m string) string {
		if loc := tokenRe.FindStringSubmatchIndex(m); loc != nil && loc[2] >= 0 {
			return m[:loc[3]] + "REDACTED"
		}
		return "gh_REDACTED"
	})
}

// RedactWriter wraps a writer (e.g. the std logger's output) so every write is
// passed through Redact. Install it with log.SetOutput(jobs.RedactWriter{W: os.Stderr}).
type RedactWriter struct{ W io.Writer }

func (r RedactWriter) Write(p []byte) (int, error) {
	if _, err := r.W.Write([]byte(Redact(string(p)))); err != nil {
		return 0, err
	}
	return len(p), nil
}
