package web

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/deemwar-products/agentstats/analyzer/jobs"
	"github.com/deemwar-products/agentstats/analyzer/store"
)

// Obviously fake credentials — never real values.
const (
	fakeClientID     = "Iv23-FAKE-CLIENT-ID"
	fakeClientSecret = "FAKE_CLIENT_SECRET_FOR_TESTS"
	fakeUserToken    = "ghu_FAKEtestTOKEN0000000000000000000000"
	fakeCode         = "fake-oauth-code"
)

// mockGitHub fakes github.com (OAuth) and api.github.com on one server.
type mockGitHub struct {
	t             *testing.T
	challenge     string // captured from the authorize redirect
	installations []map[string]any
}

func (m *mockGitHub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != fakeClientID || r.Form.Get("client_secret") != fakeClientSecret || r.Form.Get("code") != fakeCode {
			json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
			return
		}
		if pkceChallenge(r.Form.Get("code_verifier")) != m.challenge {
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": fakeUserToken, "token_type": "bearer"})
	})
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+fakeUserToken {
				http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /user", auth(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": 4242, "login": "octo", "name": "Octo Cat", "avatar_url": "https://example.test/a.png"})
	}))
	mux.HandleFunc("GET /user/emails", auth(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"email": "Octo@Example.test", "verified": true},
			{"email": "unverified@example.test", "verified": false},
		})
	}))
	mux.HandleFunc("GET /user/installations", auth(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"total_count": len(m.installations), "installations": m.installations})
	}))
	mux.HandleFunc("GET /user/installations/7/repositories", auth(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"total_count": 2, "repositories": []map[string]any{
			{"full_name": "octo/a", "clone_url": "https://github.com/octo/a.git"},
			{"full_name": "acme/secret", "clone_url": "https://github.com/acme/secret.git", "private": true},
		}})
	}))
	mux.HandleFunc("GET /user/repos", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("affiliation") != "owner,collaborator,organization_member" {
			m.t.Errorf("affiliation = %q", r.URL.Query().Get("affiliation"))
		}
		if r.URL.Query().Get("page") != "1" {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"full_name": "OCTO/a", "clone_url": "https://github.com/octo/a.git"}, // dup (case-insensitive)
			{"full_name": "octo/c", "clone_url": "https://github.com/octo/c.git", "pushed_at": "2026-09-01T00:00:00Z"},
			{"full_name": "octo/forked", "clone_url": "https://github.com/octo/forked.git", "fork": true},
		})
	}))
	return mux
}

func newTestServer(t *testing.T, gh *mockGitHub) (*Server, *store.Memory, *httptest.Server) {
	t.Helper()
	ghSrv := httptest.NewServer(gh.handler())
	t.Cleanup(ghSrv.Close)
	t.Setenv("GITHUB_CLIENT_ID", fakeClientID)
	t.Setenv("GITHUB_CLIENT_SECRET", fakeClientSecret)
	t.Setenv("GITHUB_OAUTH_BASE_URL", ghSrv.URL)
	t.Setenv("GITHUB_API_BASE_URL", ghSrv.URL)
	t.Setenv("AGENTSTATS_SITE_URL", "https://agentstats.example.test")
	t.Setenv("SESSION_KEY", "test-session-key-not-a-secret")
	t.Setenv("DEV_AUTH", "")
	st := store.NewMemory()
	runner := jobs.NewRunner(st, &jobs.Pipeline{Store: st}, "test")
	s, err := New(st, runner)
	if err != nil {
		t.Fatal(err)
	}
	return s, st, ghSrv
}

// signIn runs /auth/login then /auth/callback and returns the callback response.
func signIn(t *testing.T, s *Server, gh *mockGitHub, ghURL, loginQuery string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/auth/login"+loginQuery, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login status %d: %s", rec.Code, rec.Body)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if !strings.HasPrefix(loc.String(), ghURL+"/login/oauth/authorize?") {
		t.Fatalf("authorize redirect = %s", loc)
	}
	q := loc.Query()
	if q.Get("client_id") != fakeClientID || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != "https://agentstats.example.test/auth/callback" || q.Get("state") == "" {
		t.Fatalf("authorize query = %v", q)
	}
	gh.challenge = q.Get("code_challenge")
	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == oauthStateCookie {
			stateCookie = c
		}
	}
	if stateCookie == nil || !stateCookie.HttpOnly || !stateCookie.Secure {
		t.Fatalf("state cookie = %+v", stateCookie)
	}

	req := httptest.NewRequest("GET", "/auth/callback?code="+fakeCode+"&state="+url.QueryEscape(q.Get("state")), nil)
	req.AddCookie(stateCookie)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Result()
}

func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestOAuthCallbackSignsInAndEnqueuesFirstJob(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	gh := &mockGitHub{t: t, installations: []map[string]any{{"id": 7, "account": map[string]any{"login": "acme"}}}}
	s, st, ghSrv := newTestServer(t, gh)

	resp := signIn(t, s, gh, ghSrv.URL, "")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/repos" {
		t.Fatalf("callback = %d -> %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	sess := cookieNamed(resp, cookieName)
	if sess == nil || !sess.HttpOnly || !sess.Secure || sess.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", sess)
	}
	if id, ok := verifySession(sess.Value, s.sessionKey, time.Now().Unix()); !ok || id != 4242 {
		t.Fatalf("session verifies to %d,%v", id, ok)
	}
	if strings.Contains(sess.Value, fakeUserToken) {
		t.Fatal("token leaked into the session cookie")
	}
	if c := cookieNamed(resp, installCookie); c != nil && c.Value == "1" {
		t.Fatal("install prompt flagged although an installation exists")
	}
	u, err := st.GetUserByID(4242)
	if err != nil || u.Login != "octo" || u.Name != "Octo Cat" {
		t.Fatalf("user = %+v, %v", u, err)
	}
	if _, err := st.LatestJob(4242); err == nil {
		t.Fatal("no job may start before the user picks repos")
	}

	// The picker lists every visible repo (deduped, forks skipped); private ones are tagged.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/repos", nil)
	req.AddCookie(sess)
	s.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{"octo/a", "octo/c", "acme/secret", "private"} {
		if !strings.Contains(body, want) {
			t.Fatalf("picker missing %q", want)
		}
	}
	if strings.Contains(body, "octo/forked") || strings.Contains(body, fakeUserToken) {
		t.Fatal("picker shows a fork or leaks the token")
	}

	// Submitting nothing asks again; submitting a choice queues exactly those repos.
	if loc := submitPick(t, s, sess); loc != "/repos?none=1" {
		t.Fatalf("empty pick -> %s", loc)
	}
	if loc := submitPick(t, s, sess, "octo/c", "acme/secret"); loc != "/u/octo/status" {
		t.Fatalf("pick -> %s", loc)
	}
	job, err := st.LatestJob(4242)
	if err != nil || job.Status != store.StatusQueued || job.Kind != store.KindFirst {
		t.Fatalf("job = %+v, %v", job, err)
	}
	if strings.Contains(logs.String(), fakeUserToken) {
		t.Fatalf("token in logs: %s", logs.String())
	}

	// With stats present, a new sign-in + pick is an incremental refresh.
	_ = st.FinishJob(job.ID, store.StatusDone, "2026-09-25T00:00:00Z", 1, 1, 1, "")
	_ = st.SaveStats(store.StatsRow{GithubID: 4242, Version: 1, StatsJSON: `{"login":"octo"}`})
	resp = signIn(t, s, gh, ghSrv.URL, "?intent=refresh")
	if loc := submitPick(t, s, cookieNamed(resp, cookieName), "octo/a"); loc != "/u/octo/status" {
		t.Fatalf("refresh pick -> %s", loc)
	}
	if j, _ := st.LatestJob(4242); j.ID == job.ID || j.Kind != store.KindRefresh {
		t.Fatalf("refresh job = %+v", j)
	}
}

func submitPick(t *testing.T, s *Server, sess *http.Cookie, repos ...string) string {
	t.Helper()
	form := url.Values{}
	for _, r := range repos {
		form.Add("repo", r)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/repos", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(sess)
	s.Handler().ServeHTTP(rec, req)
	return rec.Header().Get("Location")
}

func TestOAuthCallbackFlagsMissingInstallation(t *testing.T) {
	gh := &mockGitHub{t: t, installations: []map[string]any{}}
	s, _, ghSrv := newTestServer(t, gh)
	resp := signIn(t, s, gh, ghSrv.URL, "")
	c := cookieNamed(resp, installCookie)
	if c == nil || c.Value != "1" {
		t.Fatalf("install cookie = %+v", c)
	}
	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(c)
	if !needsInstall(req) {
		t.Fatal("needsInstall false")
	}
	if got := s.gh.installURL(); got != "https://github.com/apps/agentstats-by-deemwar/installations/new" {
		t.Fatalf("install url = %s", got)
	}
}

func TestOAuthCallbackRejectsBadState(t *testing.T) {
	gh := &mockGitHub{t: t}
	s, _, _ := newTestServer(t, gh)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/auth/login", nil))
	c := rec.Result().Cookies()[0]

	req := httptest.NewRequest("GET", "/auth/callback?code="+fakeCode+"&state=forged", nil)
	req.AddCookie(c)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("forged state: %d", rec.Code)
	}

	// Tampered cookie signature.
	c.Value = strings.Replace(c.Value, "|", "x|", 1)
	req = httptest.NewRequest("GET", "/auth/callback?code="+fakeCode+"&state=x", nil)
	req.AddCookie(c)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tampered cookie: %d", rec.Code)
	}
}

func TestListReposDedupesAndSkipsForks(t *testing.T) {
	gh := &mockGitHub{t: t}
	s, _, _ := newTestServer(t, gh)
	repos, err := s.listRepos(fakeUserToken, []ghInstallation{{ID: 7}})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.FullName)
		if strings.Contains(r.CloneURL, "@") || strings.Contains(r.CloneURL, fakeUserToken) {
			t.Fatalf("credential in clone URL: %s", r.CloneURL)
		}
	}
	if got := strings.Join(names, ","); got != "octo/a,acme/secret,octo/c" {
		t.Fatalf("repos = %s", got)
	}
	if !repos[1].Private {
		t.Fatal("private flag lost")
	}
}

func TestDevAuthOnlyWithDevAuthEnv(t *testing.T) {
	gh := &mockGitHub{t: t}
	s, _, _ := newTestServer(t, gh)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/auth/dev?login=x", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/auth/dev without DEV_AUTH: %d", rec.Code)
	}
	t.Setenv("DEV_AUTH", "1")
	s2, err := New(store.NewMemory(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	s2.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/auth/dev?login=x", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("/auth/dev with DEV_AUTH=1: %d", rec.Code)
	}
}

func TestAppJWT(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemStr := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	k, err := parseAppKey(strings.ReplaceAll(pemStr, "\n", `\n`)) // flattened secret form
	if err != nil {
		t.Fatal(err)
	}
	g := githubConfig{appID: "12345", privateKey: k}
	jwt, err := g.appJWT(time.Unix(1_700_000_000, 0))
	if err != nil || strings.Count(jwt, ".") != 2 {
		t.Fatalf("jwt = %q, %v", jwt, err)
	}
}
