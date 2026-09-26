package web

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/store"
)

// statsWith builds Stats with an agent era starting 2025-01 and ending 2025-10
// (10 months), and a before era of beforeYears full years.
func statsWith(beforeYears, beforeLines, afterLines, peak int) *analyzer.Stats {
	from := 2025 - beforeYears
	return &analyzer.Stats{
		AgentStart: "2025-01",
		Eras: analyzer.Eras{
			Before: analyzer.Era{From: itoa(from), To: "2024", Lines: beforeLines},
			After:  analyzer.Era{From: "2025", To: "2025", Lines: afterLines},
		},
		Months:    map[string]int{"2025-01": 1, "2025-10": peak},
		PeakMonth: analyzer.PeakMonth{Month: "2025-10", Lines: peak},
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestEntryPaceAndEligibility(t *testing.T) {
	// 2 years before (24 months) at 1,000/mo; 10 months after at 10,000/mo → 10×.
	e := entryFor(store.User{Login: "a"}, statsWith(2, 24000, 100000, 30000))
	if e.BeforeMo != 1000 || e.AfterMo != 10000 {
		t.Fatalf("paces = %d / %d, want 1000 / 10000", e.BeforeMo, e.AfterMo)
	}
	if !e.ShiftOK || e.Shift < 9.99 || e.Shift > 10.01 {
		t.Fatalf("shift = %v ok=%v, want 10× eligible", e.Shift, e.ShiftOK)
	}
	// One year of history: not eligible for shift.
	if e := entryFor(store.User{Login: "b"}, statsWith(1, 24000, 100000, 1)); e.ShiftOK {
		t.Error("1 year before agents must not be eligible for the shift board")
	}
	// Under 5K lines before: not eligible.
	if e := entryFor(store.User{Login: "c"}, statsWith(5, 4999, 100000, 1)); e.ShiftOK {
		t.Error("<5K lines before agents must not be eligible for the shift board")
	}
}

func TestTierThresholds(t *testing.T) {
	cases := []struct {
		shift float64
		ok    bool
		want  string
	}{
		{25, true, "doesn't read the diffs"}, {20, true, "doesn't read the diffs"},
		{19.9, true, "agents go brrr"}, {8, true, "agents go brrr"},
		{7.99, true, "YOLO mode"}, {3, true, "YOLO mode"},
		{2.99, true, `"You're absolutely right!"`}, {1.5, true, `"You're absolutely right!"`},
		{1.49, true, "tab tab tab"}, {0.4, true, "tab tab tab"},
		{50, false, "new here"},
	}
	for _, c := range cases {
		if got := tierFor(lbEntry{Shift: c.shift, ShiftOK: c.ok}).Name; got != c.want {
			t.Errorf("tier(%v, ok=%v) = %q, want %q", c.shift, c.ok, got, c.want)
		}
	}
}

func lbRows(specs map[string]*analyzer.Stats, private, optedOut map[string]bool) []store.LeaderboardRow {
	var out []store.LeaderboardRow
	for login, st := range specs {
		sj, _ := json.Marshal(st)
		out = append(out, store.LeaderboardRow{
			User:  store.User{Login: login, IsPublic: !private[login], Leaderboard: !optedOut[login]},
			Stats: store.StatsRow{StatsJSON: string(sj)},
		})
	}
	return out
}

func TestBoardRankingEligibilityAndExclusion(t *testing.T) {
	bs := buildBoards(lbRows(map[string]*analyzer.Stats{
		"slow":   statsWith(3, 36000, 20000, 5000),    // 2×
		"fast":   statsWith(3, 36000, 300000, 90000),  // 30×
		"mid":    statsWith(3, 36000, 100000, 200000), // 10×, biggest peak
		"newbie": statsWith(1, 1000, 900000, 100),     // not shift-eligible, biggest output
		"hidden": statsWith(3, 36000, 900000, 900000), // private
		"optout": statsWith(3, 36000, 900000, 900000), // opted out
	}, map[string]bool{"hidden": true}, map[string]bool{"optout": true}))

	logins := func(b *lbBoard) string {
		var s []string
		for _, r := range b.Rows {
			s = append(s, r.Login)
		}
		return strings.Join(s, ",")
	}
	if got := logins(bs["shift"]); got != "fast,mid,slow" {
		t.Errorf("shift board = %s, want fast,mid,slow (newbie ineligible; hidden/optout excluded)", got)
	}
	if got := logins(bs["output"]); got != "newbie,fast,mid,slow" {
		t.Errorf("output board = %s", got)
	}
	if got := logins(bs["peak"]); got != "mid,fast,slow,newbie" {
		t.Errorf("peak board = %s", got)
	}
	for _, b := range bs {
		if _, ok := b.find("hidden"); ok {
			t.Errorf("%s: private user ranked", b.Slug)
		}
		if _, ok := b.find("optout"); ok {
			t.Errorf("%s: opted-out user ranked", b.Slug)
		}
	}
	// A tier describes the person: the same on every board.
	f1, _ := bs["shift"].find("fast")
	f2, _ := bs["peak"].find("fast")
	if f1.Tier != f2.Tier || f1.Tier.Name != "doesn't read the diffs" {
		t.Errorf("fast tier = %q / %q", f1.Tier.Name, f2.Tier.Name)
	}
	if n, _ := bs["output"].find("newbie"); n.Tier.Name != "new here" {
		t.Errorf("newbie tier = %q, want new here", n.Tier.Name)
	}
}

func TestShareText(t *testing.T) {
	r := lbRow{lbEntry: lbEntry{Login: "x", AfterLines: 1370000, Shift: 19.52, ShiftOK: true}, Rank: 35}
	if got := shareTextSelf(r); got != "#35 on agentstats: 1.37M honest lines since agents, 19.5× my old pace." {
		t.Errorf("self = %q", got)
	}
	r.Rank, r.Shift = 3, 29.9
	if got := shareTextOther(r); got != "@x is #3 on agentstats: 29.9× their old pace since agents." {
		t.Errorf("other = %q", got)
	}
}

func TestBoardCacheAndInvalidate(t *testing.T) {
	s, st, _ := newTestServer(t, &mockGitHub{t: t})
	seed := func(id int64, login string) {
		_ = st.UpsertUser(store.User{GithubID: id, Login: login, IsPublic: true})
		sj, _ := json.Marshal(statsWith(3, 36000, 100000, 1000))
		_ = st.SaveStats(store.StatsRow{GithubID: id, StatsJSON: string(sj)})
	}
	seed(1, "one")
	if n := len(s.boards()["output"].Rows); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
	seed(2, "two")
	if n := len(s.boards()["output"].Rows); n != 1 {
		t.Errorf("within 60s the cached board should be served, got %d rows", n)
	}
	s.lb.now = func() time.Time { return time.Now().Add(2 * lbTTL) }
	if n := len(s.boards()["output"].Rows); n != 2 {
		t.Errorf("after TTL the board should refresh, got %d rows", n)
	}
}

func TestLeaderboardHandlerSmoke(t *testing.T) {
	s, st, _ := newTestServer(t, &mockGitHub{t: t})
	add := func(id int64, login string, sts *analyzer.Stats) {
		_ = st.UpsertUser(store.User{GithubID: id, Login: login, IsPublic: true})
		sj, _ := json.Marshal(sts)
		_ = st.SaveStats(store.StatsRow{GithubID: id, StatsJSON: string(sj)})
	}
	add(1, "fast-dev", statsWith(3, 36000, 300000, 90000))
	add(2, "slow-dev", statsWith(3, 36000, 20000, 5000))
	add(3, "gone-dev", statsWith(3, 36000, 900000, 5000))
	_ = st.UpdateSettings(3, "auto", true, nil, false, false)

	get := func(path string, cookie *http.Cookie) string {
		req := httptest.NewRequest("GET", path, nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		b, _ := io.ReadAll(rec.Body)
		return string(b)
	}

	body := get("/leaderboard", nil)
	for _, want := range []string{"fast-dev", "slow-dev", `href="/u/fast-dev"`, "Where would you rank?", `href="/auth/login"`, "doesn&#39;t read the diffs", "We measure output, not workflow."} {
		if !strings.Contains(body, want) {
			t.Errorf("/leaderboard missing %q", want)
		}
	}
	if strings.Contains(body, "gone-dev") {
		t.Error("opted-out user shown on /leaderboard")
	}
	for _, banned := range []string{"vibe coder", "cracked", "locked in", "shipper", "software factory", "Opt in from Settings"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(banned)) {
			t.Errorf("/leaderboard contains %q", banned)
		}
	}
	if b := get("/leaderboard?q=slow", nil); strings.Contains(b, `href="/u/fast-dev"`) || !strings.Contains(b, `href="/u/slow-dev"`) {
		t.Error("?q= should filter to slow-dev")
	}
	if b := get("/leaderboard?tier=no-diffs", nil); !strings.Contains(b, `href="/u/fast-dev"`) || strings.Contains(b, `href="/u/slow-dev"`) {
		t.Error("?tier=no-diffs should show only fast-dev")
	}
	if b := get("/leaderboard?board=output", nil); !strings.Contains(b, "Agent-era output") {
		t.Error("output board should render")
	}

	// Signed in as a ranked user: the rank strip with share intents.
	sess := &http.Cookie{Name: cookieName, Value: issueSession(2, s.sessionKey, time.Now().Unix())}
	body = get("/leaderboard", sess)
	if !strings.Contains(body, "#2 <small>of 2</small>") {
		t.Error("signed-in strip should show #2 of 2")
	}
	wantText := url.QueryEscape("#2 on agentstats: 20K honest lines since agents, 2.0× my old pace.")
	if !strings.Contains(html.UnescapeString(body), wantText) {
		t.Errorf("share intent should carry the plain self text %q", wantText)
	}

	// Public profile viewed by someone else: tier pill, rank, See yours, share for them.
	body = html.UnescapeString(get("/u/fast-dev", nil))
	for _, want := range []string{"<b>#1</b> of 2", "See yours →", url.QueryEscape("@fast-dev is #1 on agentstats: 30.0× their old pace since agents.")} {
		if !strings.Contains(body, want) {
			t.Errorf("/u/fast-dev missing %q", want)
		}
	}
}

func TestSettingsSavesLeaderboardOptOut(t *testing.T) {
	s, st, _ := newTestServer(t, &mockGitHub{t: t})
	_ = st.UpsertUser(store.User{GithubID: 9, Login: "me", IsPublic: true})
	sess := &http.Cookie{Name: cookieName, Value: issueSession(9, s.sessionKey, time.Now().Unix())}

	// Settings page: the checkbox is live and checked by default.
	req := httptest.NewRequest("GET", "/settings", nil)
	req.AddCookie(sess)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `name="leaderboard" checked`) {
		t.Error("settings should show a checked leaderboard checkbox by default")
	}

	// Submitting without the box unticked opts out.
	form := url.Values{"is_public": {"on"}, "agent_auto": {"on"}}
	req = httptest.NewRequest("POST", "/api/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(sess)
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if u, _ := st.GetUserByID(9); u.Leaderboard {
		t.Error("unticked leaderboard box should opt out")
	}
}
