package web

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/jobs"
	"github.com/deemwar-products/agentstats/analyzer/render"
	"github.com/deemwar-products/agentstats/analyzer/store"
)

// ---- landing ----

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	s.render(w, "landing.html", struct {
		pageBase
		Card1 template.HTML
		Card2 template.HTML
		View  landingView
	}{
		pageBase: s.base(r),
		View:     s.landingView(),
		Card1:    template.HTML(render.BeforeAfter(s.sample, render.Light)),
		Card2:    template.HTML(render.Composition(s.sample, render.Light)),
	})
}

// ---- methodology (generated from the analyzer's own rule constants) ----

func (s *Server) handleMethodology(w http.ResponseWriter, r *http.Request) {
	notcode := make([]string, 0, len(analyzer.NOTCODE))
	for k := range analyzer.NOTCODE {
		notcode = append(notcode, k)
	}
	sort.Strings(notcode)
	s.render(w, "methodology.html", struct {
		pageBase
		MaxFileAdded   int
		MaxCommitFiles int
		MaxCommitLines int
		NotCode        []string
		LangCount      int
		SkipPattern    string
		Milestones     []analyzer.Milestone
	}{
		pageBase:       s.base(r),
		MaxFileAdded:   analyzer.MaxFileAdded,
		MaxCommitFiles: analyzer.MaxCommitFiles,
		MaxCommitLines: analyzer.MaxCommitLines,
		NotCode:        notcode,
		LangCount:      len(analyzer.LANG),
		SkipPattern:    analyzer.SKIP.String(),
		Milestones:     analyzer.Milestones(),
	})
}

// ---- profile / dashboard ----

type profileData struct {
	pageBase
	Login         string
	Name          string
	IsOwner       bool
	HasStats      bool
	Private       bool
	Headline      string
	Card1         template.HTML
	Card2         template.HTML
	Badge         template.HTML
	ExcludedTop   []analyzer.DroppedSummary
	ExcludedLines string
	BadgeURL      string
	Card1URL      string
	Card2URL      string
	ProfileURL    string
	OGImage       string
	ShareLinkedIn string
	ShareX        string
	ShareWhatsApp string
	Badges        []store.Badge
	Job           *store.Job
	StatusURL     string
	AgentStart    string
	IsPublic      bool
	// NeedsInstall: signed in, but the GitHub App is installed on no account,
	// so only repos reachable via /user/repos are analyzed. The dashboard
	// shows "Install the app on your repos" linking to InstallURL.
	NeedsInstall bool
	InstallURL   string
	View         *dashView // UI view-model (cards.go)
	Rank         *rankCard // place on the default board (leaderboard.go), if ranked
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	u, err := s.store.GetUserByID(id)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	s.renderProfile(w, r, u, true)
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	u, err := s.store.GetUserByLogin(login)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	isOwner := false
	if id, ok := s.currentUserID(r); ok && id == u.GithubID {
		isOwner = true
	}
	s.renderProfile(w, r, u, isOwner)
}

func (s *Server) renderProfile(w http.ResponseWriter, r *http.Request, u store.User, isOwner bool) {
	d := profileData{
		pageBase:   s.base(r),
		Login:      u.Login,
		Name:       u.Name,
		IsOwner:    isOwner,
		AgentStart: u.AgentStart,
		IsPublic:   u.IsPublic,
	}
	d.ProfileURL = s.site + "/u/" + u.Login
	d.OGImage = s.site + "/og/" + u.Login + ".png"
	d.BadgeURL = s.site + "/badge/" + u.Login + ".svg"
	d.Card1URL = s.site + "/card/" + u.Login + "/before-after.svg"
	d.Card2URL = s.site + "/card/" + u.Login + "/composition.svg"

	// Private profile viewed by a stranger: no cards, no data.
	if !u.IsPublic && !isOwner {
		d.Private = true
		s.render(w, "profile.html", d)
		return
	}

	if st, ok := s.statsFor(u.GithubID); ok {
		d.HasStats = true
		d.Headline = render.Card1Headline(st)
		d.Card1 = template.HTML(render.BeforeAfter(st, render.Light))
		d.Card2 = template.HTML(render.Composition(st, render.Light))
		d.Badge = template.HTML(render.Badge(st, render.Light))
		d.ExcludedTop = st.Excluded.Top
		d.ExcludedLines = render.FmtK(st.Excluded.Lines)
		d.View = s.dashViewFor(u, st, s.statsComputedAt(u.GithubID), r.URL.Query().Has("noinstall"))
		d.Rank = s.profileRank(u.Login, isOwner)
	}

	// Share intents (plain URLs, doc-13/owner add).
	text := d.Headline
	if text == "" {
		text = "My before-agents vs after-agents code stats"
	}
	d.ShareLinkedIn = "https://www.linkedin.com/sharing/share-offsite/?url=" + url.QueryEscape(d.ProfileURL)
	d.ShareX = "https://x.com/intent/post?text=" + url.QueryEscape(text) + "&url=" + url.QueryEscape(d.ProfileURL)
	d.ShareWhatsApp = "https://wa.me/?text=" + url.QueryEscape(text+" "+d.ProfileURL)

	if isOwner {
		d.Badges, _ = s.store.ListBadges(u.GithubID)
		if job, err := s.store.LatestJob(u.GithubID); err == nil {
			d.Job = &job
		}
		d.StatusURL = "/u/" + u.Login + "/status"
		d.NeedsInstall = needsInstall(r)
		d.InstallURL = s.gh.installURL()
		if d.View != nil {
			d.View.NeedsInstall = d.View.NeedsInstall || d.NeedsInstall
			if d.InstallURL != "" {
				d.View.InstallURL = d.InstallURL
			}
		}
	}
	s.render(w, "profile.html", d)
}

// handleJobStatus is the htmx-polled fragment showing queue position / progress.
func (s *Server) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	u, err := s.store.GetUserByLogin(login)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	job, err := s.store.LatestJob(u.GithubID)
	data := struct {
		Job   *store.Job
		Login string
	}{Login: login}
	if err == nil {
		data.Job = &job
	}
	s.render(w, "job_status.html", data)
}

// ---- settings ----

func (s *Server) handleSettingsPage(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	u, err := s.store.GetUserByID(id)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	s.render(w, "settings.html", struct {
		pageBase
		User store.User
	}{pageBase: s.base(r), User: u})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	_ = r.ParseForm()
	agentStart := orDefault(r.FormValue("agent_start"), "auto")
	if r.FormValue("agent_auto") != "" {
		agentStart = "auto"
	}
	isPublic := r.FormValue("is_public") == "on" || r.FormValue("is_public") == "true"
	notify := r.FormValue("notify_email") == "on"
	onBoard := r.FormValue("leaderboard") == "on" || r.FormValue("leaderboard") == "true"
	var excluded []string
	for _, e := range strings.Split(r.FormValue("excluded_repos"), "\n") {
		if e = strings.TrimSpace(e); e != "" {
			excluded = append(excluded, e)
		}
	}
	if err := s.store.UpdateSettings(id, agentStart, isPublic, excluded, notify, onBoard); err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	s.lb.invalidate() // opting out (or going private) takes effect immediately
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	u, err := s.store.GetUserByID(id)
	if err == nil {
		// Remove every R2 object under u/<login>/ (best-effort; owner-gated wiring).
		_ = s.r2DeletePrefix(u.Login)
	}
	if err := s.store.DeleteUser(id); err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// r2DeletePrefix is a thin indirection so the handler compiles without an R2
// client dependency; the pipeline's R2 client owns real deletion (owner-gated).
func (s *Server) r2DeletePrefix(login string) error {
	if s.runner != nil && s.runner.Pipeline != nil && s.runner.Pipeline.R2 != nil {
		return s.runner.Pipeline.R2.DeletePrefix(login)
	}
	return nil
}

// ---- analyze (refresh) ----

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	u, err := s.store.GetUserByID(id)
	if err != nil {
		http.Error(w, "no user", http.StatusBadRequest)
		return
	}
	// A real refresh needs a fresh user token (a new sign-in), which we do not
	// keep (docs 05/07). In dev, run over the local repo glob. In prod, the
	// refresh button links to /auth/login to obtain a fresh token.
	if s.devMode && s.devRepos != "" {
		s.startDevAnalysis(u)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<p class="muted">Refresh queued (dev). This section updates itself.</p>`))
		return
	}
	// No token in memory → send them through sign-in again to authorize a refresh.
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/auth/login?intent="+intentRefresh)
		return
	}
	http.Redirect(w, r, "/auth/login?intent="+intentRefresh, http.StatusSeeOther)
}

// ---- badge builder ----

func (s *Server) handleBadgeBuilder(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	u, err := s.store.GetUserByID(id)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	st, hasStats := s.statsFor(u.GithubID)
	preview := ""
	if hasStats {
		preview = renderBadge("agent-lines", "flat", "light", "", "", st)
	}
	s.render(w, "badges.html", struct {
		pageBase
		Login    string
		Metrics  []optItem
		Styles   []optItem
		Themes   []optItem
		Preview  template.HTML
		HasStats bool
		Badges   []savedBadgeView
	}{
		pageBase: s.base(r),
		Login:    u.Login,
		Badges:   s.savedBadgeViews(u, st),
		Metrics:  metricOptions(st, s.sample),
		Styles:   badgeStyles,
		Themes:   badgeThemes,
		Preview:  template.HTML(preview),
		HasStats: hasStats,
	})
}

func (s *Server) handleBadgePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	st, hasStats := s.statsFor(id)
	if !hasStats {
		st = s.sample // preview against the sample until the user has stats
	}
	metric := r.URL.Query().Get("metric")
	style := r.URL.Query().Get("style")
	theme := r.URL.Query().Get("theme")
	svg := renderBadge(metric, style, theme, r.URL.Query().Get("label"), "", st)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<div class="badge-preview">` + svg + `</div>`))
}

func (s *Server) handleBadgeSave(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	u, err := s.store.GetUserByID(id)
	if err != nil {
		http.Error(w, "no user", http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	b := store.Badge{
		BadgeID:   randToken()[:12],
		GithubID:  id,
		Metric:    orDefault(r.FormValue("metric"), "agent-lines"),
		Style:     orDefault(r.FormValue("style"), "flat"),
		Theme:     orDefault(r.FormValue("theme"), "light"),
		Label:     strings.TrimSpace(r.FormValue("label")),
		Period:    orDefault(r.FormValue("period"), "all"),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.store.SaveBadge(b); err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	// Render + upload the custom badge to R2 (best-effort; owner-gated wiring).
	if st, has := s.statsFor(id); has && s.runner != nil && s.runner.Pipeline != nil && s.runner.Pipeline.R2 != nil {
		svg := renderBadge(b.Metric, b.Style, b.Theme, b.Label, "", st)
		_ = s.runner.Pipeline.R2.Put(badgeR2Key(u.Login, b.BadgeID, "svg"), []byte(svg), "image/svg+xml")
	}
	http.Redirect(w, r, "/badges", http.StatusSeeOther)
}

func badgeR2Key(login, id, ext string) string {
	return "u/" + strings.ToLower(login) + "/badges/" + id + "." + ext
}

// ---- image routes (render live from stats; R2/Worker serve these in prod) ----

func (s *Server) handleCard(w http.ResponseWriter, r *http.Request) {
	login := r.PathValue("login")
	name := r.PathValue("name")
	u, err := s.store.GetUserByLogin(login)
	if err != nil || (!u.IsPublic && !s.isOwnerReq(r, u.GithubID)) {
		s.placeholder(w)
		return
	}
	st, ok := s.statsFor(u.GithubID)
	if !ok {
		s.placeholder(w)
		return
	}
	theme := r.URL.Query().Get("theme")
	if strings.Contains(name, ".dark.") {
		theme = "dark"
	}
	writeSVG(w, s.cardSVG(strings.TrimSuffix(strings.TrimSuffix(name, ".png"), ".svg"), st, theme))
}

func (s *Server) handleBadge(w http.ResponseWriter, r *http.Request) {
	login := strings.TrimSuffix(r.PathValue("login"), ".svg")
	u, err := s.store.GetUserByLogin(login)
	if err != nil || (!u.IsPublic && !s.isOwnerReq(r, u.GithubID)) {
		s.placeholder(w)
		return
	}
	st, ok := s.statsFor(u.GithubID)
	if !ok {
		s.placeholder(w)
		return
	}
	writeSVG(w, render.Badge(st, render.Light))
}

func (s *Server) handleOG(w http.ResponseWriter, r *http.Request) {
	login := strings.TrimSuffix(strings.TrimSuffix(r.PathValue("login"), ".png"), ".svg")
	u, err := s.store.GetUserByLogin(login)
	if err != nil || (!u.IsPublic && !s.isOwnerReq(r, u.GithubID)) {
		s.placeholder(w)
		return
	}
	st, ok := s.statsFor(u.GithubID)
	if !ok {
		s.placeholder(w)
		return
	}
	// Serve the SVG (PNG rasterisation needs resvg; the Worker serves the R2 PNG
	// in prod). Content stays a valid image for the preview either way.
	writeSVG(w, render.OG(st, render.Light))
}

func (s *Server) isOwnerReq(r *http.Request, githubID int64) bool {
	id, ok := s.currentUserID(r)
	return ok && id == githubID
}

func (s *Server) placeholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="480" height="120"><rect width="480" height="120" rx="10" fill="#0d1117"/><text x="24" y="54" fill="#e6edf3" font-family="system-ui" font-size="18">no stats yet</text><text x="24" y="84" fill="#8b949e" font-family="system-ui" font-size="13">agentstats.deemwar.com</text></svg>`))
}

func writeSVG(w http.ResponseWriter, svg string) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600, s-maxage=21600")
	w.Write([]byte(svg))
}

// ---- dev sign-in (local testing only; guarded by DEV_AUTH=1) ----

func (s *Server) handleAuthDev(w http.ResponseWriter, r *http.Request) {
	if !s.devMode {
		http.NotFound(w, r)
		return
	}
	login := orDefault(r.URL.Query().Get("login"), "devuser")
	gid := devID(login)
	u := store.User{GithubID: gid, Login: login, Name: login, AgentStart: "auto", IsPublic: true, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	_ = s.store.UpsertUser(u)
	// Seed the sample stats so the dashboard shows cards immediately.
	sj, _ := json.Marshal(s.sample)
	_ = s.store.SaveStats(store.StatsRow{GithubID: gid, Version: 1, ComputedAt: time.Now().UTC().Format(time.RFC3339), StatsJSON: string(sj), Headline: render.Card1Headline(s.sample)})
	setSessionCookie(w, issueSession(gid, s.sessionKey, time.Now().Unix()))
	// Optionally queue a real local analysis over the dev repo glob.
	if s.devRepos != "" {
		s.startDevAnalysis(u)
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func devID(login string) int64 {
	h := fnv.New64a()
	h.Write([]byte(login))
	// Negative ids keep dev users clearly distinct from real GitHub ids.
	return -int64(h.Sum64() & 0x7fffffff)
}

// ---- analysis kickoff ----

// startAnalysis lists the user's repos with the in-memory token and enqueues a
// job. The token rides only in the job's in-memory Spec (docs 05, 07).
func (s *Server) startAnalysis(githubID int64, login, token string, emails []string, installs []ghInstallation, agentStart string, refresh bool) error {
	if s.runner == nil {
		return fmt.Errorf("no job runner")
	}
	repos, err := s.listRepos(token, installs)
	if err != nil {
		// A partial list still yields a useful job.
		log.Printf("auth: repo listing for %s partial (%d repos): %s", login, len(repos), redactErr(err))
	}
	spec := jobs.Spec{
		GithubID:   githubID,
		Login:      login,
		Emails:     emails,
		AgentStart: agentStart,
		Token:      token,
		Repos:      repos,
		Refresh:    refresh,
	}
	_, _, err = s.runner.Submit(spec)
	return err
}

func (s *Server) startDevAnalysis(u store.User) {
	matches, _ := filepath.Glob(expandHome(s.devRepos))
	sort.Strings(matches)
	var repos []jobs.RepoSpec
	for _, m := range matches {
		repos = append(repos, jobs.RepoSpec{FullName: filepath.Base(m), GitDir: m})
	}
	spec := jobs.Spec{
		GithubID:   u.GithubID,
		Login:      u.Login,
		Emails:     devEmails(),
		AgentStart: u.AgentStart,
		Repos:      repos,
		Refresh:    true,
	}
	_, _, _ = s.runner.Submit(spec)
}

func devEmails() []string {
	if e := strings.TrimSpace(strings.ToLower(getenv("AGENTSTATS_DEV_EMAILS"))); e != "" {
		return strings.Split(e, ",")
	}
	return []string{"me@example.com"}
}

// upsertFromGitHub creates/updates the user row; returns (user, existed).
func (s *Server) upsertFromGitHub(gu ghUser) (store.User, bool) {
	existed := false
	agentStart := "auto"
	if prev, err := s.store.GetUserByID(gu.ID); err == nil {
		existed = true
		agentStart = prev.AgentStart
	}
	u := store.User{
		GithubID:   gu.ID,
		Login:      gu.Login,
		Name:       gu.Name,
		AvatarURL:  gu.AvatarURL,
		AgentStart: agentStart,
		IsPublic:   true,
	}
	_ = s.store.UpsertUser(u)
	return u, existed
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") != "" }

func randToken() string {
	// 16 random bytes hex; used for CSRF state, PKCE, badge ids.
	b := make([]byte, 16)
	_, _ = cryptoRead(b)
	const hexd = "0123456789abcdef"
	out := make([]byte, 32)
	for i, x := range b {
		out[i*2] = hexd[x>>4]
		out[i*2+1] = hexd[x&0xf]
	}
	return string(out)
}

// numFmt is available for templates if needed.
func numFmt(n int) string { return strconv.Itoa(n) }
