package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"macdoc/internal/model"
)

// ErrNoPlan is returned when no plan has the requested ID.
var ErrNoPlan = errors.New("store: no such plan")

// ErrPlanExists is returned by AddPlan when the ID is taken.
var ErrPlanExists = errors.New("store: plan ID already exists")

// PlanStatus is a plan's lifecycle state in the store. Expiry is computed
// from ExpiresAt, not stored.
type PlanStatus string

const (
	PlanPending PlanStatus = "pending"
	PlanApplied PlanStatus = "applied" // set by apply (Phase 6)
)

// PlanRecord is a plan's row: its sealed hash and summary.
type PlanRecord struct {
	ID         string       `json:"id"`
	SHA256     string       `json:"sha256"`
	ScanID     string       `json:"scan_id"`
	Origin     model.Origin `json:"origin"`
	CreatedAt  time.Time    `json:"created_at"`
	ExpiresAt  time.Time    `json:"expires_at"`
	Items      int          `json:"items"`
	TotalBytes int64        `json:"total_bytes"`
	Status     PlanStatus   `json:"status"`
}

// AddPlan records a sealed plan. It fails with ErrPlanExists if the ID is
// already used.
func (s *Store) AddPlan(r PlanRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM plans WHERE id = ?`, r.ID).Scan(&n); err != nil {
		return fmt.Errorf("store: add plan: %w", err)
	}
	if n > 0 {
		return ErrPlanExists
	}
	_, err := s.db.Exec(`INSERT INTO plans (id, sha256, scan_id, origin, created_at, expires_at, items, total_bytes, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.SHA256, r.ScanID, string(r.Origin), toDB(r.CreatedAt), toDB(r.ExpiresAt), r.Items, r.TotalBytes, string(PlanPending))
	if err != nil {
		return fmt.Errorf("store: add plan: %w", err)
	}
	return nil
}

// HasPlan reports whether a plan ID is taken.
func (s *Store) HasPlan(id string) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM plans WHERE id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("store: plan: %w", err)
	}
	return n > 0, nil
}

const planCols = `id, sha256, scan_id, origin, created_at, expires_at, items, total_bytes, status`

func scanPlan(sc interface{ Scan(...any) error }) (PlanRecord, error) {
	var r PlanRecord
	var origin, status string
	var created, expires int64
	if err := sc.Scan(&r.ID, &r.SHA256, &r.ScanID, &origin, &created, &expires, &r.Items, &r.TotalBytes, &status); err != nil {
		return r, err
	}
	r.Origin, r.Status = model.Origin(origin), PlanStatus(status)
	r.CreatedAt, r.ExpiresAt = fromDB(created), fromDB(expires)
	return r, nil
}

// Plan returns the record of plan id, or ErrNoPlan.
func (s *Store) Plan(id string) (PlanRecord, error) {
	r, err := scanPlan(s.db.QueryRow(`SELECT `+planCols+` FROM plans WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNoPlan
	}
	if err != nil {
		return r, fmt.Errorf("store: plan: %w", err)
	}
	return r, nil
}

// Plans returns up to limit plan records, newest first.
func (s *Store) Plans(limit int) ([]PlanRecord, error) {
	rows, err := s.db.Query(`SELECT `+planCols+` FROM plans ORDER BY created_at DESC, id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: plans: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []PlanRecord{}
	for rows.Next() {
		r, err := scanPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("store: plans: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ErrPlanNotPending is returned by MarkApplied when the plan was already
// applied (or is missing).
var ErrPlanNotPending = errors.New("store: plan is not pending")

// MarkApplied moves plan id from pending to applied. Apply calls it before
// acting on anything, so it is also the lock that stops a plan from being
// applied twice, even by two processes at once.
func (s *Store) MarkApplied(id string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.Exec(`UPDATE plans SET status = ? WHERE id = ? AND status = ?`,
		string(PlanApplied), id, string(PlanPending))
	if err != nil {
		return fmt.Errorf("store: mark applied: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: mark applied: %w", err)
	}
	if n != 1 {
		return ErrPlanNotPending
	}
	return nil
}
