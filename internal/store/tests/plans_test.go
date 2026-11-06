package tests

import (
	"errors"
	"github.com/sagnik26/macdoc/internal/store"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
)

func TestPlans(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	a := store.PlanRecord{ID: "P-AAAA", SHA256: "h1", ScanID: "S-1", Origin: model.OriginCLI, CreatedAt: t0, ExpiresAt: t0.Add(30 * time.Minute), Items: 2, TotalBytes: 10}
	b := a
	b.ID, b.CreatedAt, b.Origin = "P-BBBB", t0.Add(time.Minute), model.OriginClaude
	for _, r := range []store.PlanRecord{a, b} {
		if err := s.AddPlan(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddPlan(a); !errors.Is(err, store.ErrPlanExists) {
		t.Errorf("duplicate: %v", err)
	}
	got, err := s.Plan("P-AAAA")
	a.Status = store.PlanPending
	if err != nil || got != a {
		t.Errorf("plan = %+v, %v; want %+v", got, err, a)
	}
	if _, err := s.Plan("P-ZZZZ"); !errors.Is(err, store.ErrNoPlan) {
		t.Errorf("unknown: %v", err)
	}
	if ok, _ := s.HasPlan("P-BBBB"); !ok {
		t.Error("HasPlan")
	}
	list, err := s.Plans(10)
	if err != nil || len(list) != 2 || list[0].ID != "P-BBBB" || list[0].Origin != model.OriginClaude {
		t.Errorf("plans = %+v, %v", list, err)
	}
	bad := a
	bad.ID, bad.Origin = "P-CCCC", "someone"
	if err := s.AddPlan(bad); err == nil {
		t.Error("an unknown origin must be rejected by the schema")
	}
}

func TestMarkApplied(t *testing.T) {
	s := openTemp(t)
	now := time.Now()
	if err := s.AddPlan(store.PlanRecord{ID: "P-AAAA", SHA256: "x", ScanID: "s", Origin: "cli", CreatedAt: now, ExpiresAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkApplied("P-AAAA"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkApplied("P-AAAA"); !errors.Is(err, store.ErrPlanNotPending) {
		t.Errorf("second mark = %v", err)
	}
	if err := s.MarkApplied("P-NONE"); !errors.Is(err, store.ErrPlanNotPending) {
		t.Errorf("missing plan = %v", err)
	}
}

func TestQuarantineRecords(t *testing.T) {
	s := openTemp(t)
	now := time.Now()
	r := store.QuarantineRecord{ID: "Q-AAAA", PlanID: "P-AAAA", ItemID: "O-x", RecipeID: "user.logs", Original: "/h/x", Bytes: 5, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := s.AddQuarantine(r); err != nil {
		t.Fatal(err)
	}
	if err := s.AddQuarantine(r); err == nil {
		t.Error("duplicate ID accepted")
	}
	if err := s.SetQuarantineStatus("Q-AAAA", store.QuarantineDone, store.QuarantinePurged); err == nil {
		t.Error("wrong from-status accepted")
	}
	if err := s.SetQuarantineStatus("Q-AAAA", store.QuarantinePending, store.QuarantineDone); err != nil {
		t.Fatal(err)
	}
	got, err := s.Quarantine("Q-AAAA")
	if err != nil || got.Status != store.QuarantineDone || got.Original != "/h/x" || !got.ExpiresAt.Equal(r.ExpiresAt.Round(0)) {
		t.Errorf("Quarantine = %+v, %v", got, err)
	}
	if l, _ := s.QuarantineEntries(store.QuarantinePending); len(l) != 0 {
		t.Errorf("pending = %v", l)
	}
	if l, _ := s.QuarantineEntries(); len(l) != 1 {
		t.Errorf("all = %v", l)
	}
	if _, err := s.Quarantine("Q-NONE"); !errors.Is(err, store.ErrNoQuarantine) {
		t.Errorf("missing = %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE quarantine SET status = 'bogus'`); err == nil {
		t.Error("schema accepted an unknown status")
	}
}

// OpenPlans counts only pending, unexpired plans of the asked origin;
// CountPlansSince counts every plan of that origin in the window.
func TestOpenPlansAndCount(t *testing.T) {
	s := openTemp(t)
	t0 := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	add := func(id string, origin model.Origin, created time.Time, ttl time.Duration) {
		t.Helper()
		if err := s.AddPlan(store.PlanRecord{ID: id, SHA256: "h", ScanID: "S-1", Origin: origin, CreatedAt: created, ExpiresAt: created.Add(ttl)}); err != nil {
			t.Fatal(err)
		}
	}
	add("P-OLD1", model.OriginClaude, t0.Add(-time.Hour), 30*time.Minute)      // expired
	add("P-APL1", model.OriginClaude, t0.Add(-5*time.Minute), 30*time.Minute)  // applied below
	add("P-OPN1", model.OriginClaude, t0.Add(-20*time.Minute), 30*time.Minute) // open
	add("P-OPN2", model.OriginClaude, t0.Add(-2*time.Minute), 30*time.Minute)  // open
	add("P-CLI1", model.OriginCLI, t0.Add(-time.Minute), 30*time.Minute)       // other origin
	if err := s.MarkApplied("P-APL1"); err != nil {
		t.Fatal(err)
	}
	open, err := s.OpenPlans(model.OriginClaude, t0)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range open {
		ids = append(ids, r.ID)
	}
	if len(ids) != 2 || ids[0] != "P-OPN1" || ids[1] != "P-OPN2" {
		t.Errorf("open = %v, want [P-OPN1 P-OPN2]", ids)
	}
	n, err := s.CountPlansSince(model.OriginClaude, t0.Add(-10*time.Minute))
	if err != nil || n != 2 { // P-APL1 and P-OPN2
		t.Errorf("count = %d, %v; want 2", n, err)
	}
}

// store.LockPlans excludes a second holder, even in the same process (flock locks
// belong to the open file), until the first releases it.
func TestLockPlans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "state.db")
	unlock, err := store.LockPlans(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u2, err := store.LockPlans(path)
		if err != nil {
			t.Error(err)
			close(got)
			return
		}
		close(got)
		u2()
	}()
	select {
	case <-got:
		t.Fatal("second lock taken while the first was held")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("second lock never taken after release")
	}
}
