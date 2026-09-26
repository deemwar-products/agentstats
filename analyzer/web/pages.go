package web

// pages.go holds the handlers for the UI-only pages added with the approved
// design (docs/ux/index.html): the analyzing page, share previews, saved-badge
// rendering and the theme toggle. They only read from the store; nothing here
// touches auth, sessions or the job queue.

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/render"
	"github.com/deemwar-products/agentstats/analyzer/store"
)

// ---- theme ----

const themeCookie = "as_theme"

func themeFromCookie(r *http.Request) string {
	if c, err := r.Cookie(themeCookie); err == nil && (c.Value == "light" || c.Value == "dark") {
		return c.Value
	}
	return ""
}

// handleThemeToggle is the no-JS fallback for the theme button: it stores the
// choice in a cookie and sends the visitor back. The button's inline script
// does the same without a round trip.
func (s *Server) handleThemeToggle(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to")
	if to != "light" && to != "dark" {
		to = "dark"
		if themeFromCookie(r) == "dark" {
			to = "light"
		}
	}
	http.SetCookie(w, &http.Cookie{Name: themeCookie, Value: to, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode})
	back := r.URL.Query().Get("back")
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) statsComputedAt(githubID int64) string {
	row, err := s.store.GetStats(githubID)
	if err != nil {
		return ""
	}
	return row.ComputedAt
}

// ---- badges ----

// metricOptions labels each metric with the user's value ("Agent-era lines ·
// 1.25M") so the dropdown doubles as a summary.
func metricOptions(st, sample *analyzer.Stats) []optItem {
	if st == nil {
		st = sample
	}
	out := make([]optItem, 0, len(badgeMetrics))
	for _, m := range badgeMetrics {
		_, v := metricValue(m.Value, st)
		out = append(out, optItem{Value: m.Value, Label: m.Label + " · " + v})
	}
	return out
}

type savedBadgeView struct {
	ID, URL, Markdown string
	SVG               template.HTML
}

func (s *Server) savedBadgeViews(u store.User, st *analyzer.Stats) []savedBadgeView {
	bs, err := s.store.ListBadges(u.GithubID)
	if err != nil || st == nil {
		return nil
	}
	var out []savedBadgeView
	for _, b := range bs {
		url := fmt.Sprintf("%s/badge/%s/%s.svg", s.site, u.Login, b.BadgeID)
		alt := orDefault(b.Label, "agentstats")
		out = append(out, savedBadgeView{
			ID:       b.BadgeID,
			URL:      url,
			Markdown: fmt.Sprintf("[![%s](%s)](%s/u/%s)", alt, url, s.site, u.Login),
			SVG:      template.HTML(renderBadge(b.Metric, b.Style, b.Theme, b.Label, b.Accent, st)),
		})
	}
	return out
}

// handleSavedBadge renders a saved custom badge live (the Worker serves the R2
// copy in prod; this keeps README embeds working locally).
func (s *Server) handleSavedBadge(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	id := strings.TrimSuffix(r.PathValue("id"), ".svg")
	u, err := s.store.GetUserByLogin(login)
	if err != nil || (!u.IsPublic && !s.isOwnerReq(r, u.GithubID)) {
		s.placeholder(w)
		return
	}
	b, err := s.store.GetBadge(id)
	st, ok := s.statsFor(u.GithubID)
	if err != nil || b.GithubID != u.GithubID || !ok {
		s.placeholder(w)
		return
	}
	writeSVG(w, renderBadge(b.Metric, b.Style, b.Theme, b.Label, b.Accent, st))
}

// ---- analyzing / status ----

type logLine struct {
	Mark, Kind, Text string // Kind: ok | bad | wait
}

type statusView struct {
	pageBase
	Login, Name   string
	IsOwner       bool
	Job           *store.Job
	State         string // none | queued | running | done | failed
	Title         string
	Detail        string
	Pct           int
	Indeterminate bool
	Log           []logLine
	Polling       bool
	ProfileURL    string
	Href          string // link target for ProfileURL; empty means /u/<login>
	Phase         string // live phase label while running
	Tiles         []statTile
	Headline      string // done: the card headline
}

// statTile is one number on the status page (live counters while running, the summary when done).
type statTile struct{ Value, Label string }

// phaseSpan maps each live phase onto a slice of the overall progress bar.
var phaseSpan = map[string][2]int{
	"cloning": {2, 45}, "scanning": {45, 85}, "counting": {85, 95}, "rendering": {95, 99},
}

var phaseLabel = map[string]string{
	"cloning": "Cloning your repos", "scanning": "Reading commit history",
	"counting": "Counting your lines", "rendering": "Drawing your cards",
}

func elapsed(t time.Time) string {
	d := time.Since(t).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func (s *Server) statusFor(r *http.Request, u store.User) statusView {
	v := statusView{pageBase: s.base(r), Login: u.Login, Name: orDefault(u.Name, u.Login),
		IsOwner: s.isOwnerReq(r, u.GithubID), State: "none", ProfileURL: s.site + "/u/" + u.Login}
	job, err := s.store.LatestJob(u.GithubID)
	if err != nil {
		v.Title = "No analysis has run yet"
		if _, ok := s.statsFor(u.GithubID); ok {
			v.Title, v.State, v.Pct = "Your stats are ready", "done", 100
		}
		return v
	}
	v.Job = &job
	v.State = job.Status
	if job.EnqueuedAt != "" {
		v.Log = append(v.Log, logLine{"·", "wait", "queued " + agoTxt(job.EnqueuedAt)})
	}
	switch job.Status {
	case store.StatusQueued:
		v.Polling, v.Indeterminate = true, true
		v.Title = fmt.Sprintf("You're #%d in line", max(job.Position, 1))
		v.Detail = "Busy moment. You can close this tab; your profile will be ready at "
		if job.Lane == store.LaneBig {
			v.Log = append(v.Log, logLine{"·", "wait", "large account: running in the big-account lane so nobody waits behind it"})
		}
	case store.StatusRunning:
		v.Polling, v.Indeterminate = true, true
		v.Title = "Counting your commits…"
		v.Detail = "Cloning each repo and counting only the lines you wrote. You can close this tab; your profile will be ready at "
		v.Log = append(v.Log, logLine{"✓", "ok", "started " + agoTxt(job.StartedAt)})
		if s.runner != nil {
			if lv, ok := s.runner.Live(job.ID); ok {
				span := phaseSpan[lv.Phase]
				v.Indeterminate = false
				v.Pct = span[0]
				if lv.Total > 0 {
					v.Pct = span[0] + (span[1]-span[0])*lv.Done/lv.Total
				}
				v.Phase = orDefault(phaseLabel[lv.Phase], "Working")
				v.Title = v.Phase + "…"
				if lv.Total > 0 {
					v.Title = fmt.Sprintf("%s · %s / %s", v.Phase, commaInt(lv.Done), commaInt(lv.Total))
				}
				v.Detail = "You can close this tab; your profile will be ready at "
				v.Tiles = []statTile{
					{commaInt(lv.Commits), "your commits counted"},
					{render.FmtK(lv.Lines), "honest lines so far"},
					{elapsed(lv.Started), "elapsed"},
				}
				for _, r := range lv.Recent {
					v.Log = append(v.Log, logLine{"✓", "ok", r})
				}
				if lv.Current != "" {
					v.Log = append(v.Log, logLine{"…", "wait", strings.ToLower(v.Phase) + ": " + lv.Current})
				}
				break
			}
		}
		v.Log = append(v.Log, logLine{"…", "wait", "starting"})
	case store.StatusDone:
		v.Pct = 100
		if job.Repos == 0 {
			v.Title = "We couldn't read any of those repos"
			v.Detail = "Check the app is installed on the accounts that own them, then choose repos again at "
			v.ProfileURL, v.Href = s.site+"/repos", "/repos"
			v.Log = append(v.Log, logLine{"✗", "bad", "0 repos read"})
			return v
		}
		v.Title = "Your stats are ready"
		v.Detail = fmt.Sprintf("Analyzed %s repos and %s commits in %ds. Your profile is at ", commaInt(job.Repos), commaInt(job.Commits), job.Seconds)
		if st, ok := s.statsFor(u.GithubID); ok {
			v.Headline = render.Card1Headline(st)
			b, a := st.Eras.Before, st.Eras.After
			v.Tiles = []statTile{
				{render.FmtK(b.Lines), "lines before agents (" + b.Label() + ")"},
				{render.FmtK(a.Lines), "lines after agents (" + a.Label() + ")"},
				{render.FmtK(st.PeakMonth.Lines), "peak month " + st.PeakMonth.Month},
				{render.FmtK(st.Excluded.Lines), "lines of imports & generated code excluded"},
			}
		}
		v.Log = append(v.Log, logLine{"✓", "ok", fmt.Sprintf("done: %s repos · %s commits · %ds", commaInt(job.Repos), commaInt(job.Commits), job.Seconds)})
	case store.StatusFailed:
		v.Title = "The last run failed"
		v.Detail = "Nothing was saved from the failed run. Try again from your dashboard, or check "
		v.Log = append(v.Log, logLine{"✗", "bad", orDefault(job.Error, "unknown error")})
	}
	return v
}

// handleStatusPage is the full "analyzing" page; htmx polls of the same URL
// (HX-Request) get just the fragment, which stops polling once the job ends.
func (s *Server) handleStatusPage(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUserByLogin(r.PathValue("login"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v := s.statusFor(r, u)
	if isHTMX(r) {
		s.render(w, "job_status.html", v)
		return
	}
	s.render(w, "status.html", v)
}

// ---- share previews ----

type shareView struct {
	pageBase
	Login, Name   string
	Private       bool
	HasStats      bool
	Headline      string
	OGImage       string // absolute, for meta
	OGPath        string // relative, for the page
	ProfileURL    string
	Links         shareLinks
	LinkedInText  string
	XText         string
	WhatsAppTitle string
}

func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUserByLogin(r.PathValue("login"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name := orDefault(u.Name, u.Login)
	v := shareView{pageBase: s.base(r), Login: u.Login, Name: name,
		ProfileURL: s.site + "/u/" + u.Login,
		OGImage:    s.site + "/og/" + u.Login + ".png",
		OGPath:     "/og/" + u.Login + ".png",
	}
	if !u.IsPublic && !s.isOwnerReq(r, u.GithubID) {
		v.Private = true
		s.render(w, "share.html", v)
		return
	}
	if st, ok := s.statsFor(u.GithubID); ok {
		b, a := st.Eras.Before, st.Eras.After
		v.HasStats = true
		v.Headline = render.Card1Headline(st)
		v.LinkedInText = fmt.Sprintf("Ran my whole GitHub history through an honest counter. %s before agents: %s lines. %s after: %s.",
			capFirst(plural(b.YearCount(), "year")), render.FmtK(b.Lines),
			plural(monthsBetween(st.AgentStart, lastMonthOf(st)), "month"), render.FmtK(a.Lines))
		v.XText = v.Headline + " Imports and generated code excluded."
		if by, best := bestBeforeYear(st); st.PeakMonth.Month != "" && best > 0 {
			v.XText = fmt.Sprintf("%s: %s lines. My best full year before agents was %s (%s). Imports and generated code excluded.",
				monthLong(st.PeakMonth.Month), render.FmtK(st.PeakMonth.Lines), render.FmtK(best), by)
		}
		v.WhatsAppTitle = name + ": before vs after agents"
	}
	v.Links = makeShareLinks(v.ProfileURL, orDefault(v.Headline, "My before-agents vs after-agents code stats"))
	s.render(w, "share.html", v)
}
