package jobs

import (
	"sync"
	"time"
)

// Live is a snapshot of a running job's progress, kept in memory only and shown on the
// status page so a user never stares at an empty bar.
type Live struct {
	Phase   string // cloning | scanning | counting | rendering | saving
	Done    int
	Total   int
	Current string // repo being worked on (display name; private repos masked)
	Commits int    // your commits counted so far
	Lines   int    // honest lines counted so far
	Recent  []string
	Started time.Time
}

// LiveProgress is the mutable, concurrency-safe tracker behind Live.
type LiveProgress struct {
	mu sync.Mutex
	v  Live
}

func newLiveProgress() *LiveProgress {
	return &LiveProgress{v: Live{Phase: "cloning", Started: time.Now()}}
}

// Step records progress. A nil receiver is a no-op so callers need no checks.
func (p *LiveProgress) Step(phase, current string, done, total, commits, lines int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if phase != p.v.Phase {
		p.v.Recent = nil
	}
	p.v.Phase, p.v.Done, p.v.Total = phase, done, total
	if commits >= 0 {
		p.v.Commits = commits
	}
	if lines >= 0 {
		p.v.Lines = lines
	}
	if current != "" && current != p.v.Current {
		p.v.Current = current
		p.v.Recent = append(p.v.Recent, current)
		if len(p.v.Recent) > 5 {
			p.v.Recent = p.v.Recent[len(p.v.Recent)-5:]
		}
	}
}

// Snapshot returns a copy safe to read without the lock.
func (p *LiveProgress) Snapshot() Live {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.v
	c.Recent = append([]string(nil), p.v.Recent...)
	return c
}
