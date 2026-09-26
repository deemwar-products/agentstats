// Package web is the agentstats server-rendered UI, GitHub sign-in, dashboard,
// badge builder, and the analysis-job lifecycle — the whole backend in one Go
// service (doc 12). Templates are html/template + plain CSS + htmx (loaded from
// a CDN) for small interactions. No TypeScript, no build step.
package web

import (
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"strings"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/jobs"
	"github.com/deemwar-products/agentstats/analyzer/render"
	"github.com/deemwar-products/agentstats/analyzer/store"
)

//go:embed templates/*.html
var tmplFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server holds the wired dependencies.
type Server struct {
	store      store.Store
	runner     *jobs.Runner
	tmpl       *template.Template
	sessionKey []byte
	gh         githubConfig
	devMode    bool
	devRepos   string // local *.git glob for dev analyses (AGENTSTATS_DEV_REPOS)
	site       string
	sample     *analyzer.Stats
	lb         *lbCache // computed leaderboard boards, 60s
	handler    http.Handler
}

// New builds the Server. sessionKey signs session cookies (SESSION_KEY secret).
func New(st store.Store, runner *jobs.Runner) (*Server, error) {
	key := os.Getenv("SESSION_KEY")
	if key == "" {
		// Local/dev fallback so the app runs without secrets. In prod SESSION_KEY
		// is a real secret; an obviously-fake dev default never ships live.
		key = "dev-only-insecure-session-key-set-SESSION_KEY-in-prod"
	}
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"raw": func(s string) template.HTML { return template.HTML(s) },
	}).ParseFS(tmplFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{
		store:      st,
		runner:     runner,
		tmpl:       tmpl,
		sessionKey: []byte(key),
		gh:         ghConfigFromEnv(),
		devMode:    os.Getenv("DEV_AUTH") == "1",
		devRepos:   os.Getenv("AGENTSTATS_DEV_REPOS"),
		site:       strings.TrimRight(orDefault(os.Getenv("PUBLIC_BASE_URL"), ghConfigFromEnv().siteURL), "/"),
		sample:     sampleStats(),
		lb:         newLBCache(),
	}
	if s.devMode {
		s.seedDevBoard(getenv("AGENTSTATS_DEV_SEED"))
	}
	s.routes()
	return s, nil
}

// Handler returns the composed http.Handler.
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) routes() {
	mux := http.NewServeMux()

	// Static assets.
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	// Pages.
	mux.HandleFunc("GET /{$}", s.handleLanding)
	mux.HandleFunc("GET /methodology", s.handleMethodology)
	mux.HandleFunc("GET /dashboard", s.handleDashboard)
	mux.HandleFunc("GET /settings", s.handleSettingsPage)
	mux.HandleFunc("GET /badges", s.handleBadgeBuilder)
	mux.HandleFunc("GET /u/{login}", s.handleProfile)
	mux.HandleFunc("GET /u/{login}/status", s.handleStatusPage) // full page; htmx polls get the fragment
	mux.HandleFunc("GET /share/{login}", s.handleSharePage)
	mux.HandleFunc("GET /leaderboard", s.handleLeaderboard)
	mux.HandleFunc("GET /theme", s.handleThemeToggle)

	// Auth.
	mux.HandleFunc("GET /auth/login", s.handleAuthLogin)
	mux.HandleFunc("GET /auth/callback", s.handleAuthCallback)
	mux.HandleFunc("GET /auth/installed", s.handleAuthInstalled)
	mux.HandleFunc("GET /repos", s.handleRepoPicker)
	mux.HandleFunc("POST /repos", s.handleRepoPickerSubmit)
	mux.HandleFunc("POST /auth/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /auth/dev", s.handleAuthDev) // guarded by devMode inside

	// API (session-guarded).
	mux.HandleFunc("POST /api/analyze", s.handleAnalyze)
	mux.HandleFunc("POST /api/settings", s.handleSettings)
	mux.HandleFunc("POST /api/delete", s.handleDelete)
	mux.HandleFunc("GET /api/badge-preview", s.handleBadgePreview)
	mux.HandleFunc("POST /api/badges", s.handleBadgeSave)

	// Image routes — served from R2 by the front Worker in prod; the Go server
	// renders them live here so the UI and README embeds work locally too.
	mux.HandleFunc("GET /card/{login}/{name}", s.handleCard)
	mux.HandleFunc("GET /badge/{login}", s.handleBadge)
	mux.HandleFunc("GET /badge/{login}/{id}", s.handleSavedBadge)
	mux.HandleFunc("GET /og/{login}", s.handleOG)

	// Health probe.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	s.handler = mux
}

// ---- page data + rendering helpers ----

type pageBase struct {
	Site     string
	Footer   string
	Deemwar  string
	SignedIn bool
	Me       string // login of the signed-in user, if any
	Theme    string // "light" | "dark" | "" (follow the OS), from the theme cookie
	Path     string // request path, for active-nav state
}

func (s *Server) base(r *http.Request) pageBase {
	pb := pageBase{Site: s.site, Footer: "agentstats by deemwar", Deemwar: "https://cli.deemwar.com", Theme: themeFromCookie(r), Path: r.URL.Path}
	if id, ok := s.currentUserID(r); ok {
		if u, err := s.store.GetUserByID(id); err == nil {
			pb.SignedIn = true
			pb.Me = u.Login
		}
	}
	return pb
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// statsFor loads and unmarshals a user's stored Stats.
func (s *Server) statsFor(githubID int64) (*analyzer.Stats, bool) {
	row, err := s.store.GetStats(githubID)
	if err != nil {
		return nil, false
	}
	var st analyzer.Stats
	if json.Unmarshal([]byte(row.StatsJSON), &st) != nil {
		return nil, false
	}
	st.NormalizeEras()
	return &st, true
}

// cardSVG renders a named artifact live from Stats (inline SVG for pages, and
// the image routes). theme "dark" selects the dark palette.
func (s *Server) cardSVG(name string, st *analyzer.Stats, theme string) string {
	t := render.Light
	if theme == "dark" {
		t = render.Dark
	}
	switch {
	case strings.HasPrefix(name, "before-after"):
		return render.BeforeAfter(st, t)
	case strings.HasPrefix(name, "composition"):
		return render.Composition(st, t)
	case strings.HasPrefix(name, "og"):
		return render.OG(st, t)
	default:
		return render.Badge(st, t)
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
