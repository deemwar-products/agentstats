package web

// leaderboard.go is the real leaderboard (design: docs/ux/leaderboard.html).
// Public by default: every public user with stats is ranked unless they opted
// out in Settings. Boards are computed from the stored stats JSON and cached
// in memory for 60s; tiers are percentiles on the board being viewed.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/deemwar-products/agentstats/analyzer"
	"github.com/deemwar-products/agentstats/analyzer/render"
	"github.com/deemwar-products/agentstats/analyzer/store"
)

// Shift-board eligibility: enough pre-agent history that a new account can't
// game the multiplier with a tiny "before".
const (
	shiftMinBeforeMonths = 24
	shiftMinBeforeLines  = 5000
	lbTTL                = 60 * time.Second
)

// ---- tiers ----

// tier is an ABSOLUTE stage by pace multiplier (after-era monthly pace ÷
// before-era monthly pace). It describes the person, not their position, so it
// is the same on every board. We measure output, not workflow.
type tier struct {
	Level      int    // 5 = fastest; 0 = not enough history
	Slug, Name string // Slug is the ?tier= value
	Rule       string
	Min        float64 // lowest multiplier in this tier
}

// tiers is the one table of names and thresholds (fastest first). Edit here.
var tiers = []tier{
	{5, "no-diffs", "doesn't read the diffs", "20× or more", 20},
	{4, "brrr", "agents go brrr", "8–20×", 8},
	{3, "yolo", "YOLO mode", "3–8×", 3},
	{2, "absolutely-right", "\"You're absolutely right!\"", "1.5–3×", 1.5},
	{1, "tab", "tab tab tab", "under 1.5×", 0},
}

// tierNew is for people without 2+ years and 5K+ lines before agents: no
// multiplier, no stage.
var tierNew = tier{0, "new", "new here", "under 2 years or 5K lines before agents", 0}

// allTiers is the filter-card order.
var allTiers = append(append([]tier{}, tiers...), tierNew)

// tierFor places an entry by its multiplier.
func tierFor(e lbEntry) tier {
	if !e.ShiftOK {
		return tierNew
	}
	for _, t := range tiers {
		if e.Shift >= t.Min {
			return t
		}
	}
	return tiers[len(tiers)-1]
}

func tierBySlug(slug string) (tier, bool) {
	for _, t := range allTiers {
		if t.Slug == slug {
			return t, true
		}
	}
	return tier{}, false
}

// ---- entries and boards ----

// lbEntry is one developer's leaderboard numbers, derived from their Stats.
type lbEntry struct {
	GithubID          int64
	Login, Name       string
	AvatarURL         string
	BeforeMo, AfterMo int     // lines per month in each era
	Shift             float64 // AfterMo / BeforeMo
	ShiftOK           bool    // eligible for the shift board
	AfterLines        int
	PeakLines         int
	PeakMonth         string
}

// lbRow is an entry ranked on one board.
type lbRow struct {
	lbEntry
	Rank  int
	Tier  tier
	Value string // formatted board value
}

func (r lbRow) Initial() string {
	if r.Login == "" {
		return "?"
	}
	return strings.ToUpper(r.Login[:1])
}

// AvatarColor gives initials-avatars a stable colour per login.
func (r lbRow) AvatarColor() string {
	colors := []string{"#c8553d", "#4a6fa5", "#3f7d5c", "#e0a526", "#8a6bb0", "#2f8f9d"}
	return colors[len(r.Login)%len(colors)]
}

func (r lbRow) BeforeMoTxt() string { return fmtOrDash(r.BeforeMo) }
func (r lbRow) AfterMoTxt() string  { return fmtOrDash(r.AfterMo) }

func fmtOrDash(n int) string {
	if n <= 0 {
		return "—"
	}
	return render.FmtK(n)
}

type lbBoard struct {
	Slug, Label string
	Rows        []lbRow
	byLogin     map[string]int // lower(login) -> index in Rows
}

func (b *lbBoard) find(login string) (lbRow, bool) {
	if b == nil {
		return lbRow{}, false
	}
	i, ok := b.byLogin[strings.ToLower(login)]
	if !ok {
		return lbRow{}, false
	}
	return b.Rows[i], true
}

type boardDef struct {
	Slug, Label string
	eligible    func(lbEntry) bool
	value       func(lbEntry) float64
	format      func(lbEntry) string
}

var boardDefs = []boardDef{
	{"shift", "Biggest shift",
		func(e lbEntry) bool { return e.ShiftOK },
		func(e lbEntry) float64 { return e.Shift },
		func(e lbEntry) string { return fmtShift(e.Shift) }},
	{"output", "Agent-era output",
		func(e lbEntry) bool { return e.AfterLines > 0 },
		func(e lbEntry) float64 { return float64(e.AfterLines) },
		func(e lbEntry) string { return render.FmtK(e.AfterLines) }},
	{"peak", "Peak month",
		func(e lbEntry) bool { return e.PeakLines > 0 },
		func(e lbEntry) float64 { return float64(e.PeakLines) },
		func(e lbEntry) string { return render.FmtK(e.PeakLines) }},
}

func boardDefFor(slug string) boardDef {
	for _, d := range boardDefs {
		if d.Slug == slug {
			return d
		}
	}
	return boardDefs[0]
}

func fmtShift(x float64) string { return fmt.Sprintf("%.1f×", x) }

// entryFor derives leaderboard numbers from one user's Stats.
func entryFor(u store.User, st *analyzer.Stats) lbEntry {
	b, a := st.Eras.Before, st.Eras.After
	e := lbEntry{GithubID: u.GithubID, Login: u.Login, Name: u.Name, AvatarURL: u.AvatarURL,
		AfterLines: a.Lines, PeakLines: st.PeakMonth.Lines, PeakMonth: st.PeakMonth.Month}
	bm := beforeMonths(st)
	am := 0
	if _, m := parseYM(st.AgentStart); m > 0 {
		am = monthsBetween(st.AgentStart, lastMonthOf(st))
	}
	if bm > 0 {
		e.BeforeMo = b.Lines / bm
	}
	if am > 0 {
		e.AfterMo = a.Lines / am
	}
	if bm > 0 && am > 0 && b.Lines > 0 {
		e.Shift = (float64(a.Lines) / float64(am)) / (float64(b.Lines) / float64(bm))
	}
	e.ShiftOK = bm >= shiftMinBeforeMonths && b.Lines >= shiftMinBeforeLines && e.Shift > 0
	return e
}

// beforeMonths is the length of the pre-agent era in months: from the first
// month with counted lines (or Jan of the before era's first year) up to the
// month before the agent era starts.
func beforeMonths(st *analyzer.Stats) int {
	ay, am := parseYM(st.AgentStart)
	if am == 0 {
		return 0
	}
	first := ""
	for k, v := range st.Months {
		if v > 0 && k < st.AgentStart && (first == "" || k < first) {
			first = k
		}
	}
	if from := st.Eras.Before.From; len(from) == 4 && from+"-01" < st.AgentStart && (first == "" || from+"-01" < first) {
		first = from + "-01"
	}
	fy, fm := parseYM(first)
	if fm == 0 {
		return 0
	}
	n := (ay*12 + am) - (fy*12 + fm)
	if n < 0 {
		return 0
	}
	return n
}

// buildBoard ranks the eligible entries on one board (desc, ties by login).
func buildBoard(def boardDef, entries []lbEntry) *lbBoard {
	var el []lbEntry
	for _, e := range entries {
		if def.eligible(e) {
			el = append(el, e)
		}
	}
	sort.SliceStable(el, func(i, j int) bool {
		vi, vj := def.value(el[i]), def.value(el[j])
		if vi != vj {
			return vi > vj
		}
		return strings.ToLower(el[i].Login) < strings.ToLower(el[j].Login)
	})
	b := &lbBoard{Slug: def.Slug, Label: def.Label, Rows: make([]lbRow, len(el)), byLogin: map[string]int{}}
	for i, e := range el {
		b.Rows[i] = lbRow{lbEntry: e, Rank: i + 1, Tier: tierFor(e), Value: def.format(e)}
		b.byLogin[strings.ToLower(e.Login)] = i
	}
	return b
}

// buildBoards turns store rows into every board. Opted-out and private users
// are already filtered by the store; they are filtered again here so a store
// bug can never leak them onto the page.
func buildBoards(rows []store.LeaderboardRow) map[string]*lbBoard {
	var entries []lbEntry
	for _, r := range rows {
		if !r.User.IsPublic || !r.User.Leaderboard {
			continue
		}
		var st analyzer.Stats
		if json.Unmarshal([]byte(r.Stats.StatsJSON), &st) != nil {
			continue
		}
		st.NormalizeEras()
		entries = append(entries, entryFor(r.User, &st))
	}
	out := map[string]*lbBoard{}
	for _, d := range boardDefs {
		out[d.Slug] = buildBoard(d, entries)
	}
	return out
}

// ---- 60s in-memory cache ----

type lbCache struct {
	mu     sync.Mutex
	at     time.Time
	boards map[string]*lbBoard
	now    func() time.Time
}

func newLBCache() *lbCache { return &lbCache{now: time.Now} }

func (c *lbCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.boards = nil
	c.mu.Unlock()
}

func (s *Server) boards() map[string]*lbBoard {
	c := s.lb
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.boards != nil && c.now().Sub(c.at) < lbTTL {
		return c.boards
	}
	rows, err := s.store.LeaderboardRows()
	if err != nil {
		if c.boards != nil {
			return c.boards // serve stale rather than an empty board
		}
		return buildBoards(nil)
	}
	c.boards, c.at = buildBoards(rows), c.now()
	return c.boards
}

// ---- share text (plain, no hype) ----

func shareTextSelf(r lbRow) string {
	if r.ShiftOK {
		return fmt.Sprintf("#%d on agentstats: %s honest lines since agents, %s my old pace.", r.Rank, render.FmtK(r.AfterLines), fmtShift(r.Shift))
	}
	return fmt.Sprintf("#%d on agentstats: %s honest lines since agents.", r.Rank, render.FmtK(r.AfterLines))
}

func shareTextOther(r lbRow) string {
	if r.ShiftOK {
		return fmt.Sprintf("@%s is #%d on agentstats: %s their old pace since agents.", r.Login, r.Rank, fmtShift(r.Shift))
	}
	return fmt.Sprintf("@%s is #%d on agentstats: %s honest lines since agents.", r.Login, r.Rank, render.FmtK(r.AfterLines))
}

// rankCard is a person's place on a board, ready for a strip or a chip.
type rankCard struct {
	Row        lbRow
	Board      string // board label
	BoardSlug  string
	Of         int
	ProfileURL string
	Text       string
	Links      shareLinks
}

func (s *Server) rankCardFor(b *lbBoard, row lbRow, self bool) *rankCard {
	rc := &rankCard{Row: row, Board: b.Label, BoardSlug: b.Slug, Of: len(b.Rows), ProfileURL: s.site + "/u/" + row.Login}
	rc.Text = shareTextOther(row)
	if self {
		rc.Text = shareTextSelf(row)
	}
	rc.Links = makeShareLinks(rc.ProfileURL, rc.Text)
	return rc
}

// profileRank is a user's place on the default (shift) board, falling back to
// output when they aren't eligible for shift.
func (s *Server) profileRank(login string, self bool) *rankCard {
	bs := s.boards()
	for _, slug := range []string{"shift", "output"} {
		if row, ok := bs[slug].find(login); ok {
			return s.rankCardFor(bs[slug], row, self)
		}
	}
	return nil
}

// ---- page ----

type lbTab struct {
	Slug, Label, Href string
	On                bool
}

type tierCard struct {
	T     tier
	Count int
	On    bool
	Href  string
}

type lbView struct {
	pageBase
	Boards    []lbTab
	Board     *lbBoard
	Tiers     []tierCard
	Tier      string
	ClearTier string // same board + search, no tier filter
	Q         string
	Rows      []lbRow
	Total     int
	Mine      *rankCard
	MeNote    string // signed in but not ranked on this board: why
	MeLink    string
	MeLinkT   string
}

func lbHref(board, tierSlug, q string) string {
	v := url.Values{}
	if board != "" && board != "shift" {
		v.Set("board", board)
	}
	if tierSlug != "" {
		v.Set("tier", tierSlug)
	}
	if q != "" {
		v.Set("q", q)
	}
	if len(v) == 0 {
		return "/leaderboard"
	}
	return "/leaderboard?" + v.Encode()
}

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	def := boardDefFor(r.URL.Query().Get("board"))
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 64 {
		q = q[:64]
	}
	sel, hasTier := tierBySlug(r.URL.Query().Get("tier"))
	board := s.boards()[def.Slug]

	v := lbView{pageBase: s.base(r), Board: board, Q: q, Total: len(board.Rows)}
	if hasTier {
		v.Tier = sel.Slug
	}
	v.ClearTier = lbHref(def.Slug, "", q)
	for _, d := range boardDefs {
		v.Boards = append(v.Boards, lbTab{Slug: d.Slug, Label: d.Label, Href: lbHref(d.Slug, v.Tier, q), On: d.Slug == def.Slug})
	}
	counts := map[string]int{}
	for _, row := range board.Rows {
		counts[row.Tier.Slug]++
	}
	for _, t := range allTiers {
		on := hasTier && t.Slug == sel.Slug
		href := lbHref(def.Slug, t.Slug, q)
		if on {
			href = lbHref(def.Slug, "", q) // clicking the active tier clears it
		}
		v.Tiers = append(v.Tiers, tierCard{T: t, Count: counts[t.Slug], On: on, Href: href})
	}
	lq := strings.ToLower(q)
	for _, row := range board.Rows {
		if hasTier && row.Tier.Slug != sel.Slug {
			continue
		}
		if lq != "" && !strings.Contains(strings.ToLower(row.Login), lq) && !strings.Contains(strings.ToLower(row.Name), lq) {
			continue
		}
		v.Rows = append(v.Rows, row)
	}

	if v.SignedIn {
		if row, ok := board.find(v.Me); ok {
			v.Mine = s.rankCardFor(board, row, true)
		} else {
			v.MeNote, v.MeLink, v.MeLinkT = s.whyNotRanked(r, def)
		}
	}
	s.render(w, "leaderboard.html", v)
}

// whyNotRanked explains to a signed-in user why they aren't on this board.
func (s *Server) whyNotRanked(r *http.Request, def boardDef) (note, link, linkText string) {
	id, _ := s.currentUserID(r)
	u, err := s.store.GetUserByID(id)
	if err != nil {
		return "", "", ""
	}
	switch {
	case !u.IsPublic:
		return "Your profile is private, so you aren't ranked.", "/settings", "Settings"
	case !u.Leaderboard:
		return "You've hidden yourself from the leaderboard.", "/settings", "Settings"
	}
	st, ok := s.statsFor(u.GithubID)
	if !ok {
		return "You'll be ranked once your first analysis finishes.", "/dashboard", "Dashboard"
	}
	if def.Slug == "shift" && !entryFor(u, st).ShiftOK {
		return "Biggest shift needs 2+ years and 5K+ lines before agents. You're ranked on the other boards.", lbHref("output", "", ""), "Agent-era output"
	}
	return "You'll appear here within a minute.", "", ""
}
