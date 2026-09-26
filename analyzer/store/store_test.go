package store

import (
	"testing"
	"time"
)

func TestEnqueueSingleFlight(t *testing.T) {
	m := NewMemory()
	j1, existing, err := m.EnqueueJob(7, KindFirst, LaneNormal, "2026-01-01T00:00:00Z")
	if err != nil || existing {
		t.Fatalf("first enqueue: existing=%v err=%v", existing, err)
	}
	j2, existing, _ := m.EnqueueJob(7, KindRefresh, LaneNormal, "2026-01-01T00:01:00Z")
	if !existing {
		t.Fatalf("second enqueue should return the existing job")
	}
	if j2.ID != j1.ID {
		t.Fatalf("expected same job id, got %d and %d", j1.ID, j2.ID)
	}
}

func TestQueueFairnessPosition(t *testing.T) {
	m := NewMemory()
	// A refresh enqueued first, then a first-timer: the first-timer must rank ahead.
	rf, _, _ := m.EnqueueJob(1, KindRefresh, LaneNormal, "2026-01-01T00:00:00Z")
	ft, _, _ := m.EnqueueJob(2, KindFirst, LaneNormal, "2026-01-01T00:00:30Z")
	if ft.Position != 1 {
		t.Errorf("first-time job position = %d, want 1 (ahead of refresh)", ft.Position)
	}
	if rf2, _ := m.GetJob(rf.ID); rf2.Position != 2 {
		t.Errorf("refresh position = %d, want 2", rf2.Position)
	}
}

func TestClaimAndFinish(t *testing.T) {
	m := NewMemory()
	j, _, _ := m.EnqueueJob(5, KindFirst, LaneNormal, "2026-01-01T00:00:00Z")
	ok, _ := m.ClaimJob(j.ID, "inst-a", "2026-01-01T00:00:01Z")
	if !ok {
		t.Fatal("claim should succeed on a queued job")
	}
	ok, _ = m.ClaimJob(j.ID, "inst-b", "2026-01-01T00:00:02Z")
	if ok {
		t.Fatal("second claim on a running job must fail")
	}
	if err := m.FinishJob(j.ID, StatusDone, "2026-01-01T00:05:00Z", 42, 100, 30, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetJob(j.ID)
	if got.Status != StatusDone || got.Repos != 42 {
		t.Errorf("finished job = %+v", got)
	}
}

func TestReclaimStale(t *testing.T) {
	m := NewMemory()
	j, _, _ := m.EnqueueJob(9, KindFirst, LaneNormal, "2026-01-01T00:00:00Z")
	_, _ = m.ClaimJob(j.ID, "inst", "2026-01-01T00:00:00Z") // heartbeat is old
	n, _ := m.ReclaimStale("2026-01-01T00:10:00Z", "2026-01-01T00:11:00Z")
	if n != 1 {
		t.Fatalf("reclaimed %d, want 1", n)
	}
	got, _ := m.GetJob(j.ID)
	if got.Status != StatusFailed {
		t.Errorf("stale job status = %q, want failed", got.Status)
	}
	// The single-flight slot is freed: a new job can be enqueued.
	if _, existing, _ := m.EnqueueJob(9, KindRefresh, LaneNormal, "2026-01-01T00:12:00Z"); existing {
		t.Error("after reclaim the user should be able to enqueue again")
	}
}

func TestCacheHitAndInvalidate(t *testing.T) {
	m := NewMemory()
	_ = m.UpsertUser(User{GithubID: 3, Login: "alice", IsPublic: true})
	c := NewCache(m, time.Minute)

	// Prime the cache.
	if _, err := c.GetUserByID(3); err != nil {
		t.Fatal(err)
	}
	// Mutate the backing store directly; the cache should still serve the old value.
	_ = m.UpsertUser(User{GithubID: 3, Login: "alice", Name: "changed", IsPublic: true})
	u, _ := c.GetUserByID(3)
	if u.Name == "changed" {
		t.Error("cache should have served the primed value")
	}
	// A write through the cache invalidates it.
	_ = c.UpsertUser(User{GithubID: 3, Login: "alice", Name: "changed2", IsPublic: true})
	u, _ = c.GetUserByID(3)
	if u.Name != "changed2" {
		t.Errorf("after cache write-through, name = %q, want changed2", u.Name)
	}
}
