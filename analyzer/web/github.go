package web

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/deemwar-products/agentstats/analyzer/jobs"
)

// githubConfig is the GitHub App config (docs 05). Every value comes from the
// container env/secrets at runtime; nothing is committed.
//
//	GITHUB_APP_ID, GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET, GITHUB_APP_PRIVATE_KEY
//	AGENTSTATS_SITE_URL   public base URL (callback = <site>/auth/callback)
//	GITHUB_OAUTH_BASE_URL / GITHUB_API_BASE_URL   overrides for tests only
type githubConfig struct {
	appID        string
	appSlug      string
	clientID     string
	clientSecret string
	privateKey   *rsa.PrivateKey // App key, for installation tokens (weekly refresh)
	siteURL      string          // e.g. https://agentstats.deemwar.com
	oauthBase    string          // https://github.com
	apiBase      string          // https://api.github.com
}

const defaultAppSlug = "agentstats-by-deemwar"

func ghConfigFromEnv() githubConfig {
	g := githubConfig{
		appID:        os.Getenv("GITHUB_APP_ID"),
		appSlug:      orDefault(os.Getenv("GITHUB_APP_SLUG"), defaultAppSlug),
		clientID:     os.Getenv("GITHUB_CLIENT_ID"),
		clientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
		siteURL:      strings.TrimRight(orDefault(os.Getenv("AGENTSTATS_SITE_URL"), "https://agentstats.deemwar.com"), "/"),
		oauthBase:    strings.TrimRight(orDefault(os.Getenv("GITHUB_OAUTH_BASE_URL"), "https://github.com"), "/"),
		apiBase:      strings.TrimRight(orDefault(os.Getenv("GITHUB_API_BASE_URL"), "https://api.github.com"), "/"),
	}
	if pemStr := os.Getenv("GITHUB_APP_PRIVATE_KEY"); pemStr != "" {
		if k, err := parseAppKey(pemStr); err == nil {
			g.privateKey = k
		} else {
			log.Printf("github: GITHUB_APP_PRIVATE_KEY present but unparseable: %v", err)
		}
	}
	return g
}

func (g githubConfig) configured() bool {
	return g.clientID != "" && g.clientSecret != ""
}

// installURL is where a user grants the App access to their repos.
func (g githubConfig) installURL() string {
	return "https://github.com/apps/" + g.appSlug + "/installations/new"
}

// oauthStateCookie is the short-lived signed cookie holding CSRF state, the
// PKCE verifier and the sign-in intent between /auth/login and /auth/callback.
const oauthStateCookie = "as_oauth"

// installCookie flags "signed in, but the App is installed nowhere" so the
// dashboard can show the install prompt on any instance. Not sensitive.
const installCookie = "as_noinstall"

// retryStatePrefix marks the OAuth state of an automatic sign-in restart.
const retryStatePrefix = "r-"

// intentRefresh marks a sign-in started from the Refresh button: the fresh
// token is used for a re-analysis even when stats already exist.
const intentRefresh = "refresh"

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.gh.configured() {
		msg := "sign-in not configured: set GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET (docs 05)."
		if s.devMode {
			msg += " Dev sign-in: /auth/dev?login=you"
		}
		http.Error(w, msg, http.StatusServiceUnavailable)
		return
	}
	state := randToken()
	if r.URL.Query().Get("retried") == "1" {
		state = retryStatePrefix + state
	}
	verifier := randToken() + randToken() // 64 hex chars: within PKCE's 43..128
	intent := ""
	if r.URL.Query().Get("intent") == intentRefresh {
		intent = intentRefresh
	}

	payload := state + "|" + verifier + "|" + intent
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: payload + "|" + sign(payload, s.sessionKey), Path: "/auth",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})

	q := url.Values{}
	q.Set("client_id", s.gh.clientID)
	q.Set("redirect_uri", s.gh.siteURL+"/auth/callback")
	q.Set("state", state)
	q.Set("code_challenge", pkceChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	http.Redirect(w, r, s.gh.oauthBase+"/login/oauth/authorize?"+q.Encode(), http.StatusFound)
}

func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !s.gh.configured() {
		http.Error(w, "sign-in not configured", http.StatusServiceUnavailable)
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		// User declined on GitHub.
		clearOAuthCookie(w)
		http.Redirect(w, r, "/?signin="+url.QueryEscape(e), http.StatusFound)
		return
	}
	c, err := r.Cookie(oauthStateCookie)
	if err != nil {
		// Stale or cross-tab start (the state cookie expired or lives in another tab/profile).
		// Restart once: GitHub remembers the grant, so this round-trip is instant for the user.
		// A retried sign-in carries retryStatePrefix in its state (GitHub echoes state back),
		// which stops a loop when cookies are genuinely blocked.
		if !strings.HasPrefix(r.URL.Query().Get("state"), retryStatePrefix) {
			http.Redirect(w, r, "/auth/login?retried=1", http.StatusFound)
			return
		}
		http.Error(w, "sign-in needs cookies: please allow cookies for agentstats.deemwar.com and try again", http.StatusBadRequest)
		return
	}
	parts := strings.Split(c.Value, "|")
	if len(parts) != 4 || !hmac.Equal([]byte(sign(strings.Join(parts[:3], "|"), s.sessionKey)), []byte(parts[3])) {
		http.Error(w, "bad oauth state", http.StatusBadRequest)
		return
	}
	state, verifier, intent := parts[0], parts[1], parts[2]
	if !hmac.Equal([]byte(r.URL.Query().Get("state")), []byte(state)) {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	clearOAuthCookie(w)

	// The user token lives only in this request and the job's in-memory Spec.
	// Never persisted, never logged (docs 05, 07).
	token, err := s.exchangeCode(code, verifier)
	if err != nil {
		log.Printf("auth: token exchange failed: %s", jobs.Redact(err.Error()))
		http.Error(w, "GitHub token exchange failed", http.StatusBadGateway)
		return
	}
	gu, err := s.ghGetUser(token)
	if err != nil || gu.ID == 0 || gu.Login == "" {
		log.Printf("auth: GET /user failed: %v", redactErr(err))
		http.Error(w, "GitHub /user failed", http.StatusBadGateway)
		return
	}
	emails, err := s.ghGetEmails(token) // verified only; job memory only
	if err != nil {
		log.Printf("auth: GET /user/emails for %s: %v", gu.Login, redactErr(err))
	}

	u, _ := s.upsertFromGitHub(gu)
	setSessionCookie(w, issueSession(u.GithubID, s.sessionKey, time.Now().Unix()))

	installs, err := s.ghListInstallations(token)
	if err != nil {
		log.Printf("auth: GET /user/installations for %s: %v", gu.Login, redactErr(err))
	}
	noInstall := err == nil && len(installs) == 0
	setInstallCookie(w, noInstall)
	if noInstall {
		// The App can read nothing until it is installed, so go straight to GitHub's install
		// screen (defaults to All repositories, public + private). GitHub then redirects to
		// the Setup URL /auth/installed, which re-signs-in silently and starts the analysis.
		http.Redirect(w, r, s.gh.installURL(), http.StatusFound)
		return
	}

	// Hand off to the repo picker: list everything the App can see, hold the token in
	// memory for the pick (docs 05/07), and let the user choose which repos to count.
	_, hasStats := s.statsFor(u.GithubID)
	repos, lerr := s.listRepos(token, installs)
	if lerr != nil {
		log.Printf("auth: repo listing for %s partial (%d repos): %s", gu.Login, len(repos), redactErr(lerr))
	}
	putPick(u.GithubID, &pendingPick{
		login: u.Login, token: token, emails: emails, repos: repos,
		agentStart: u.AgentStart, refresh: hasStats || intent == intentRefresh,
	})
	http.Redirect(w, r, "/repos", http.StatusFound)
}

// handleAuthInstalled is the GitHub App Setup URL: GitHub sends the user here after they
// install (or change) the App. A silent re-sign-in mints a token that can see the newly
// granted repos; the callback then enqueues a fresh analysis (intent=refresh).
func (s *Server) handleAuthInstalled(w http.ResponseWriter, r *http.Request) {
	setInstallCookie(w, false)
	http.Redirect(w, r, "/auth/login?intent="+intentRefresh, http.StatusFound)
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	setInstallCookie(w, false)
	http.Redirect(w, r, "/", http.StatusFound)
}

// exchangeCode swaps the OAuth code for a user-to-server token.
func (s *Server) exchangeCode(code, verifier string) (string, error) {
	form := url.Values{}
	form.Set("client_id", s.gh.clientID)
	form.Set("client_secret", s.gh.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", s.gh.siteURL+"/auth/callback")
	form.Set("code_verifier", verifier)

	req, _ := http.NewRequest(http.MethodPost, s.gh.oauthBase+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "agentstats")
	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("access_token: status %d", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("no access_token: %s", out.Error)
	}
	return out.AccessToken, nil
}

// ghUser is the subset of GET /user we keep.
type ghUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

func (s *Server) ghGetUser(token string) (ghUser, error) {
	var u ghUser
	err := s.ghGetJSON("/user", token, &u)
	return u, err
}

func (s *Server) ghGetEmails(token string) ([]string, error) {
	var raw []struct {
		Email    string `json:"email"`
		Verified bool   `json:"verified"`
	}
	if err := s.ghGetJSON("/user/emails?per_page=100", token, &raw); err != nil {
		return nil, err
	}
	var out []string
	for _, e := range raw {
		if e.Verified && e.Email != "" {
			out = append(out, strings.ToLower(e.Email))
		}
	}
	return out, nil
}

// ghInstallation is the subset of GET /user/installations we use.
type ghInstallation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

// ghListInstallations returns the App installations this user can access.
func (s *Server) ghListInstallations(token string) ([]ghInstallation, error) {
	var all []ghInstallation
	for page := 1; page <= 10; page++ {
		var out struct {
			Installations []ghInstallation `json:"installations"`
		}
		if err := s.ghGetJSON(fmt.Sprintf("/user/installations?per_page=100&page=%d", page), token, &out); err != nil {
			return all, err
		}
		all = append(all, out.Installations...)
		if len(out.Installations) < 100 {
			break
		}
	}
	return all, nil
}

// ghRepo is the subset of a GitHub repository object we use.
type ghRepo struct {
	FullName string `json:"full_name"`
	CloneURL string `json:"clone_url"`
	Private  bool   `json:"private"`
	Fork     bool   `json:"fork"`
	PushedAt string `json:"pushed_at"`
}

// maxReposPerJob is the per-job repo cap (docs 07).
const maxReposPerJob = 500

// listRepos returns the repos to analyze (docs 05): everything the App
// installations grant, plus repos the user owns / collaborates on / reaches via
// org membership. Deduped by full_name, forks skipped, capped at 500. A partial
// list is returned alongside the first error so one failing call never blocks
// the job.
func (s *Server) listRepos(token string, installs []ghInstallation) ([]jobs.RepoSpec, error) {
	seen := map[string]bool{}
	var specs []jobs.RepoSpec
	var firstErr error
	add := func(rs []ghRepo) {
		for _, r := range rs {
			key := strings.ToLower(r.FullName)
			if r.Fork || r.FullName == "" || seen[key] || len(specs) >= maxReposPerJob {
				continue
			}
			seen[key] = true
			specs = append(specs, jobs.RepoSpec{FullName: r.FullName, CloneURL: r.CloneURL, Private: r.Private, PushedAt: r.PushedAt})
		}
	}

	for _, in := range installs {
		for page := 1; page <= 20; page++ {
			var out struct {
				Repositories []ghRepo `json:"repositories"`
			}
			if err := s.ghGetJSON(fmt.Sprintf("/user/installations/%d/repositories?per_page=100&page=%d", in.ID, page), token, &out); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				break
			}
			add(out.Repositories)
			if len(out.Repositories) < 100 {
				break
			}
		}
	}

	for page := 1; page <= 20; page++ {
		var repos []ghRepo
		if err := s.ghGetJSON(fmt.Sprintf("/user/repos?affiliation=owner,collaborator,organization_member&per_page=100&page=%d", page), token, &repos); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		add(repos)
		if len(repos) < 100 {
			break
		}
	}
	return specs, firstErr
}

// ghGetJSON GETs apiBase+path with the user token and decodes JSON into v.
// Errors carry the path and status, never the token.
func (s *Server) ghGetJSON(path, token string, v any) error {
	req, _ := http.NewRequest(http.MethodGet, s.gh.apiBase+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "agentstats")
	resp, err := httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: status %d: %s", strings.SplitN(path, "?", 2)[0], resp.StatusCode, jobs.Redact(string(b)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func httpClient() *http.Client { return &http.Client{Timeout: 20 * time.Second} }

func redactErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return jobs.Redact(err.Error())
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func clearOAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Value: "", Path: "/auth", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

func setInstallCookie(w http.ResponseWriter, needsInstall bool) {
	c := &http.Cookie{Name: installCookie, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}
	if needsInstall {
		c.Value, c.MaxAge = "1", maxAgeSecs
	} else {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

func needsInstall(r *http.Request) bool {
	c, err := r.Cookie(installCookie)
	return err == nil && c.Value == "1"
}

// ---- App JWT (for installation tokens; the weekly refresh path, docs 05) ----

func parseAppKey(pemStr string) (*rsa.PrivateKey, error) {
	// Secrets stores often flatten newlines; accept a literal "\n" form too.
	pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return rk, nil
}

// appJWT signs the short-lived RS256 JWT that authenticates as the App itself
// (iat backdated 60 s for clock skew, 9 min lifetime; GitHub allows 10).
func (g githubConfig) appJWT(now time.Time) (string, error) {
	if g.privateKey == nil || g.appID == "" {
		return "", errors.New("github app key/id not configured")
	}
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": g.appID,
	})
	signing := hdr + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, g.privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
