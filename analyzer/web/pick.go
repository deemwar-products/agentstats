package web

import (
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/deemwar-products/agentstats/analyzer/jobs"
)

// pendingPick holds what a signed-in user needs to start an analysis between the OAuth
// callback and the moment they choose repos on /repos. The token stays in process memory
// only (docs 05/07) and expires with the pick.
type pendingPick struct {
	login      string
	token      string
	emails     []string
	repos      []jobs.RepoSpec
	agentStart string
	refresh    bool
	expires    time.Time
}

const pickTTL = 30 * time.Minute

var picks = struct {
	sync.Mutex
	m map[int64]*pendingPick
}{m: map[int64]*pendingPick{}}

func putPick(id int64, p *pendingPick) {
	p.expires = time.Now().Add(pickTTL)
	picks.Lock()
	defer picks.Unlock()
	for k, v := range picks.m { // opportunistic sweep
		if time.Now().After(v.expires) {
			delete(picks.m, k)
		}
	}
	picks.m[id] = p
}

func getPick(id int64) *pendingPick {
	picks.Lock()
	defer picks.Unlock()
	p := picks.m[id]
	if p == nil || time.Now().After(p.expires) {
		delete(picks.m, id)
		return nil
	}
	return p
}

func dropPick(id int64) {
	picks.Lock()
	defer picks.Unlock()
	delete(picks.m, id)
}

type pickRepo struct {
	FullName string
	Name     string
	Private  bool
	Pushed   string // YYYY-MM-DD
}

type pickGroup struct {
	Owner string
	Repos []pickRepo
}

// handleRepoPicker shows every repo the App can see, grouped by owner, newest push first.
func (s *Server) handleRepoPicker(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/auth/login?intent="+intentRefresh, http.StatusFound)
		return
	}
	p := getPick(id)
	if p == nil {
		// No token in memory (expired or the instance restarted): a silent re-sign-in mints one.
		http.Redirect(w, r, "/auth/login?intent="+intentRefresh, http.StatusFound)
		return
	}
	byOwner := map[string][]pickRepo{}
	private := 0
	for _, rs := range p.repos {
		owner, name, _ := strings.Cut(rs.FullName, "/")
		pushed := rs.PushedAt
		if len(pushed) >= 10 {
			pushed = pushed[:10]
		}
		if rs.Private {
			private++
		}
		byOwner[owner] = append(byOwner[owner], pickRepo{FullName: rs.FullName, Name: name, Private: rs.Private, Pushed: pushed})
	}
	var groups []pickGroup
	for owner, rs := range byOwner {
		sort.Slice(rs, func(i, j int) bool { return rs[i].Pushed > rs[j].Pushed })
		groups = append(groups, pickGroup{Owner: owner, Repos: rs})
	}
	sort.Slice(groups, func(i, j int) bool {
		if (groups[i].Owner == p.login) != (groups[j].Owner == p.login) {
			return groups[i].Owner == p.login // the user's own account first
		}
		return groups[i].Owner < groups[j].Owner
	})
	s.render(w, "repos.html", struct {
		pageBase
		Groups     []pickGroup
		Total      int
		Private    int
		Public     int
		InstallURL string
		None       bool
	}{pageBase: s.base(r), Groups: groups, Total: len(p.repos), Private: private, Public: len(p.repos) - private,
		InstallURL: s.gh.installURL(), None: r.URL.Query().Has("none")})
}

// handleRepoPickerSubmit starts the analysis over exactly the repos the user ticked.
func (s *Server) handleRepoPickerSubmit(w http.ResponseWriter, r *http.Request) {
	id, ok := s.currentUserID(r)
	if !ok {
		http.Redirect(w, r, "/auth/login?intent="+intentRefresh, http.StatusSeeOther)
		return
	}
	p := getPick(id)
	if p == nil {
		http.Redirect(w, r, "/auth/login?intent="+intentRefresh, http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	want := map[string]bool{}
	for _, v := range r.Form["repo"] {
		want[strings.ToLower(v)] = true
	}
	var selected []jobs.RepoSpec
	for _, rs := range p.repos {
		if want[strings.ToLower(rs.FullName)] {
			selected = append(selected, rs)
		}
	}
	if len(selected) == 0 {
		http.Redirect(w, r, "/repos?none=1", http.StatusSeeOther)
		return
	}
	if s.runner == nil {
		http.Error(w, "no job runner", http.StatusServiceUnavailable)
		return
	}
	spec := jobs.Spec{
		GithubID:   id,
		Login:      p.login,
		Emails:     p.emails,
		AgentStart: p.agentStart,
		Token:      p.token,
		Repos:      selected,
		Refresh:    p.refresh,
	}
	if _, _, err := s.runner.Submit(spec); err != nil {
		log.Printf("repos: submit for %s: %s", p.login, redactErr(err))
		http.Error(w, "could not start the analysis, please try again", http.StatusInternalServerError)
		return
	}
	dropPick(id)
	http.Redirect(w, r, "/u/"+p.login+"/status", http.StatusSeeOther)
}
