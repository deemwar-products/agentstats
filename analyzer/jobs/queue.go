package jobs

import (
	"sort"
	"sync"
	"time"

	"github.com/deemwar-products/agentstats/analyzer/store"
)

// Runner is the per-instance job scheduler. The jobs table is the shared,
// visible queue (doc 13); this Runner claims and executes queued jobs it holds
// the in-memory Spec for (the user token lives only in this instance's memory,
// so an instance only runs jobs it accepted). It enforces the per-instance
// concurrency cap and the fairness rules.
type Runner struct {
	Store    store.Store
	Pipeline *Pipeline
	Instance string

	// MaxConcurrent is N, the per-instance job cap. Global cap = max_instances×N.
	// max_instances is the COST CEILING (raise deliberately) — doc 13.
	MaxConcurrent int
	// BigLaneMax caps concurrent big-account (>300 repos) jobs so they never
	// starve the normal lane (doc 13 fairness).
	BigLaneMax int

	HeartbeatEvery time.Duration
	StaleAfter     time.Duration
	Tick           time.Duration

	mu      sync.Mutex
	specs   map[int64]Spec          // jobID -> spec (token/emails/repos; in memory only)
	running map[int64]string        // jobID -> lane, for this instance
	live    map[int64]*LiveProgress // jobID -> in-memory progress
	stop    chan struct{}
}

// NewRunner builds a Runner with sensible defaults.
func NewRunner(st store.Store, p *Pipeline, instance string) *Runner {
	return &Runner{
		Store:          st,
		Pipeline:       p,
		Instance:       instance,
		MaxConcurrent:  3,
		BigLaneMax:     1,
		HeartbeatEvery: 15 * time.Second,
		StaleAfter:     3 * time.Minute,
		Tick:           1 * time.Second,
		specs:          map[int64]Spec{},
		running:        map[int64]string{},
		stop:           make(chan struct{}),
	}
}

// Submit enqueues an analysis for a user. It is single-flight: if the user
// already has a queued/running job, that job is returned (existing=true) and no
// new spec is stored. Returns the job with its queue Position for the UI.
func (r *Runner) Submit(spec Spec) (store.Job, bool, error) {
	kind := store.KindFirst
	if spec.Refresh {
		kind = store.KindRefresh
	}
	lane := store.LaneNormal
	if len(spec.Repos) > store.BigAccountRepos {
		lane = store.LaneBig
	}
	job, existing, err := r.Store.EnqueueJob(spec.GithubID, kind, lane, nowUTC())
	if err != nil {
		return store.Job{}, false, err
	}
	if !existing {
		r.mu.Lock()
		r.specs[job.ID] = spec
		r.mu.Unlock()
	}
	return job, existing, nil
}

// Start runs the scheduler loop until Stop.
func (r *Runner) Start() {
	go func() {
		t := time.NewTicker(r.Tick)
		defer t.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-t.C:
				r.schedule()
			}
		}
	}()
}

// Stop halts the scheduler loop.
func (r *Runner) Stop() { close(r.stop) }

// schedule reclaims stale jobs, then claims and starts queued jobs up to the
// caps, in fairness order (first-time before refresh, then FIFO; big lane
// bounded separately).
func (r *Runner) schedule() {
	stale := time.Now().UTC().Add(-r.StaleAfter).Format("2006-01-02T15:04:05Z")
	_, _ = r.Store.ReclaimStale(stale, nowUTC())

	// Candidate jobs = queued jobs whose spec this instance holds.
	queued, err := r.Store.QueuedJobs()
	if err != nil {
		return
	}
	r.mu.Lock()
	var mine []store.Job
	for _, j := range queued {
		if _, ok := r.specs[j.ID]; ok {
			mine = append(mine, j)
		}
	}
	// Fairness order.
	sort.Slice(mine, func(i, j int) bool { return fairLess(mine[i], mine[j]) })
	// Current per-instance usage.
	total := len(r.running)
	big := 0
	for _, lane := range r.running {
		if lane == store.LaneBig {
			big++
		}
	}
	var toStart []store.Job
	for _, j := range mine {
		if total >= r.MaxConcurrent {
			break
		}
		if j.Lane == store.LaneBig && big >= r.BigLaneMax {
			continue
		}
		toStart = append(toStart, j)
		total++
		if j.Lane == store.LaneBig {
			big++
		}
		r.running[j.ID] = j.Lane // reserve the slot; released in run()
	}
	// Grab specs before unlocking.
	specs := map[int64]Spec{}
	for _, j := range toStart {
		specs[j.ID] = r.specs[j.ID]
	}
	r.mu.Unlock()

	for _, j := range toStart {
		ok, err := r.Store.ClaimJob(j.ID, r.Instance, nowUTC())
		if err != nil || !ok {
			// Lost the race (another instance) — release the reserved slot.
			r.release(j.ID)
			continue
		}
		go r.run(j, specs[j.ID])
	}
}

// run executes one claimed job with a heartbeat, then records the outcome.
func (r *Runner) run(job store.Job, spec Spec) {
	defer r.release(job.ID)

	hbStop := make(chan struct{})
	go func() {
		t := time.NewTicker(r.HeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-t.C:
				_ = r.Store.Heartbeat(job.ID, nowUTC())
			}
		}
	}()

	lp := newLiveProgress()
	r.mu.Lock()
	if r.live == nil {
		r.live = map[int64]*LiveProgress{}
	}
	r.live[job.ID] = lp
	r.mu.Unlock()
	spec.Live = lp
	res, err := r.Pipeline.Run(spec, nil)
	close(hbStop)

	if err != nil {
		_ = r.Store.FinishJob(job.ID, store.StatusFailed, nowUTC(), 0, 0, 0, err.Error())
		return
	}
	_ = r.Store.FinishJob(job.ID, store.StatusDone, nowUTC(), res.Repos, res.Commits, res.Seconds, "")
}

func (r *Runner) release(jobID int64) {
	r.mu.Lock()
	delete(r.running, jobID)
	delete(r.specs, jobID)
	delete(r.live, jobID)
	r.mu.Unlock()
}

// fairLess: first-time analyses before refreshes, then oldest enqueued, then id.
func fairLess(a, b store.Job) bool {
	if a.Kind != b.Kind {
		return a.Kind == store.KindFirst
	}
	if a.EnqueuedAt != b.EnqueuedAt {
		return a.EnqueuedAt < b.EnqueuedAt
	}
	return a.ID < b.ID
}

// Live returns the in-memory progress of a job running on this instance.
func (r *Runner) Live(jobID int64) (Live, bool) {
	r.mu.Lock()
	lp := r.live[jobID]
	r.mu.Unlock()
	if lp == nil {
		return Live{}, false
	}
	return lp.Snapshot(), true
}
