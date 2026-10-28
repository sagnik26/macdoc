package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"macdoc/internal/model"
)

func TestPlans(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	a := PlanRecord{ID: "P-AAAA", SHA256: "h1", ScanID: "S-1", Origin: model.OriginCLI, CreatedAt: t0, ExpiresAt: t0.Add(30 * time.Minute), Items: 2, TotalBytes: 10}
	b := a
	b.ID, b.CreatedAt, b.Origin = "P-BBBB", t0.Add(time.Minute), model.OriginClaude
	for _, r := range []PlanRecord{a, b} {
		if err := s.AddPlan(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddPlan(a); !errors.Is(err, ErrPlanExists) {
		t.Errorf("duplicate: %v", err)
	}
	got, err := s.Plan("P-AAAA")
	a.Status = PlanPending
	if err != nil || got != a {
		t.Errorf("plan = %+v, %v; want %+v", got, err, a)
	}
	if _, err := s.Plan("P-ZZZZ"); !errors.Is(err, ErrNoPlan) {
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
