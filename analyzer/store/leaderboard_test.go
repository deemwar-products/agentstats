package store

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestMigration0003AddsLeaderboardDefaultOn(t *testing.T) {
	b, err := os.ReadFile("../../migrations/0003_leaderboard.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ALTER TABLE users ADD COLUMN leaderboard INTEGER NOT NULL DEFAULT 1") {
		t.Errorf("0003 must add users.leaderboard defaulting to 1 (opt-out), got:\n%s", b)
	}
}

func TestMemoryLeaderboardDefaultsOnAndSurvivesUpsert(t *testing.T) {
	m := NewMemory()
	_ = m.UpsertUser(User{GithubID: 1, Login: "a", IsPublic: true}) // Leaderboard not set by caller
	u, _ := m.GetUserByID(1)
	if !u.Leaderboard {
		t.Fatal("a new user must be on the leaderboard by default")
	}
	if err := m.UpdateSettings(1, "auto", true, nil, false, false); err != nil {
		t.Fatal(err)
	}
	_ = m.UpsertUser(User{GithubID: 1, Login: "a", IsPublic: true, Leaderboard: true}) // re-login
	if u, _ = m.GetUserByID(1); u.Leaderboard {
		t.Error("re-login (UpsertUser) must not undo an opt-out")
	}
	_ = m.UpdateSettings(1, "auto", true, nil, false, true)
	if u, _ = m.GetUserByID(1); !u.Leaderboard {
		t.Error("UpdateSettings(leaderboard=true) should opt back in")
	}
}

func TestMemoryLeaderboardRowsExcludesPrivateOptedOutAndStatless(t *testing.T) {
	m := NewMemory()
	for id, login := range map[int64]string{1: "on", 2: "private", 3: "optedout", 4: "nostats"} {
		_ = m.UpsertUser(User{GithubID: id, Login: login, IsPublic: true})
		if id != 4 {
			_ = m.SaveStats(StatsRow{GithubID: id, StatsJSON: "{}"})
		}
	}
	_ = m.UpdateSettings(2, "auto", false, nil, false, true)
	_ = m.UpdateSettings(3, "auto", true, nil, false, false)
	rows, err := m.LeaderboardRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].User.Login != "on" {
		t.Fatalf("rows = %+v, want only 'on'", rows)
	}
}

// fakeD1 records the SQL sent to the internal route and replies with rows.
func fakeD1(t *testing.T, reply []map[string]any, seen *[]d1Request) *D1 {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req d1Request
		_ = json.NewDecoder(r.Body).Decode(&req)
		*seen = append(*seen, req)
		_ = json.NewEncoder(w).Encode(d1Response{Success: true, Results: reply})
	}))
	t.Cleanup(srv.Close)
	return &D1{url: srv.URL, secret: "FAKE_SECRET", http: srv.Client()}
}

func TestD1LeaderboardFieldReadWrite(t *testing.T) {
	var seen []d1Request
	d := fakeD1(t, []map[string]any{{"github_id": 7.0, "login": "x", "is_public": 1.0, "leaderboard": 0.0}}, &seen)

	u, err := d.GetUserByID(7)
	if err != nil {
		t.Fatal(err)
	}
	if u.Leaderboard {
		t.Error("leaderboard=0 row should read as Leaderboard=false")
	}
	if !strings.Contains(seen[0].SQL, "leaderboard") {
		t.Errorf("user SELECT must read leaderboard: %s", seen[0].SQL)
	}

	if err := d.UpdateSettings(7, "auto", true, nil, false, true); err != nil {
		t.Fatal(err)
	}
	last := seen[len(seen)-1]
	if !strings.Contains(last.SQL, "leaderboard=?") {
		t.Errorf("UpdateSettings must write leaderboard: %s", last.SQL)
	}
	// params: agent_start, is_public, excluded, notify, leaderboard, id
	if got := last.Params[4]; got != 1.0 {
		t.Errorf("leaderboard param = %v, want 1", got)
	}

	if err := d.UpsertUser(User{GithubID: 7, Login: "x"}); err != nil {
		t.Fatal(err)
	}
	if up := seen[len(seen)-1].SQL; strings.Contains(up, "leaderboard") {
		t.Errorf("UpsertUser must leave leaderboard to the DB default / existing value: %s", up)
	}
}

func TestD1LeaderboardRowsFiltersInSQL(t *testing.T) {
	var seen []d1Request
	d := fakeD1(t, []map[string]any{{"github_id": 7.0, "login": "x", "is_public": 1.0, "leaderboard": 1.0, "stats_json": "{}"}}, &seen)
	rows, err := d.LeaderboardRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].User.Login != "x" || rows[0].Stats.StatsJSON != "{}" || !rows[0].User.Leaderboard {
		t.Fatalf("rows = %+v", rows)
	}
	sql := seen[0].SQL
	for _, want := range []string{"u.is_public = 1", "u.leaderboard = 1", "JOIN stats"} {
		if !strings.Contains(sql, want) {
			t.Errorf("LeaderboardRows SQL missing %q: %s", want, sql)
		}
	}
}
