package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNoQuarantine is returned when no quarantine entry has the requested ID.
var ErrNoQuarantine = errors.New("store: no such quarantine entry")

// QuarantineStatus is a quarantine entry's lifecycle state.
type QuarantineStatus string

const (
	QuarantinePending  QuarantineStatus = "pending"  // recorded; the move may not have happened
	QuarantineDone     QuarantineStatus = "done"     // moved into quarantine
	QuarantineCanceled QuarantineStatus = "canceled" // apply stopped before the move and tidied up
	QuarantineRestored QuarantineStatus = "restored" // moved back
	QuarantinePurged   QuarantineStatus = "purged"   // permanently deleted
)

// QuarantineRecord is macdoc's own record of a quarantine entry.
type QuarantineRecord struct {
	ID        string           `json:"id"`
	PlanID    string           `json:"plan_id"`
	ItemID    string           `json:"item_id"`
	RecipeID  string           `json:"recipe_id"`
	Original  string           `json:"original"`
	Bytes     int64            `json:"bytes"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt time.Time        `json:"expires_at"`
	Status    QuarantineStatus `json:"status"`
}

// AddQuarantine records a new entry as pending. It fails if the ID is taken.
func (s *Store) AddQuarantine(r QuarantineRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`INSERT INTO quarantine (id, plan_id, item_id, recipe_id, original, bytes, created_at, expires_at, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.PlanID, r.ItemID, r.RecipeID, r.Original, r.Bytes, toDB(r.CreatedAt), toDB(r.ExpiresAt), string(QuarantinePending))
	if err != nil {
		return fmt.Errorf("store: add quarantine: %w", err)
	}
	return nil
}

// SetQuarantineStatus moves entry id from one status to another. It fails
// if the entry isn't in the from status.
func (s *Store) SetQuarantineStatus(id string, from, to QuarantineStatus) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.db.Exec(`UPDATE quarantine SET status = ? WHERE id = ? AND status = ?`, string(to), id, string(from))
	if err != nil {
		return fmt.Errorf("store: quarantine status: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("store: quarantine status: %w", err)
	} else if n != 1 {
		return fmt.Errorf("store: quarantine %s is not %s", id, from)
	}
	return nil
}

// HasQuarantine reports whether a quarantine ID is taken.
func (s *Store) HasQuarantine(id string) (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM quarantine WHERE id = ?`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("store: quarantine: %w", err)
	}
	return n > 0, nil
}

const quarantineCols = `id, plan_id, item_id, recipe_id, original, bytes, created_at, expires_at, status`

func scanQuarantine(sc interface{ Scan(...any) error }) (QuarantineRecord, error) {
	var r QuarantineRecord
	var status string
	var created, expires int64
	if err := sc.Scan(&r.ID, &r.PlanID, &r.ItemID, &r.RecipeID, &r.Original, &r.Bytes, &created, &expires, &status); err != nil {
		return r, err
	}
	r.Status = QuarantineStatus(status)
	r.CreatedAt, r.ExpiresAt = fromDB(created), fromDB(expires)
	return r, nil
}

// Quarantine returns the record of entry id, or ErrNoQuarantine.
func (s *Store) Quarantine(id string) (QuarantineRecord, error) {
	r, err := scanQuarantine(s.db.QueryRow(`SELECT `+quarantineCols+` FROM quarantine WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNoQuarantine
	}
	if err != nil {
		return r, fmt.Errorf("store: quarantine: %w", err)
	}
	return r, nil
}

// QuarantineEntries returns entries in the given statuses (all if none),
// oldest first.
func (s *Store) QuarantineEntries(statuses ...QuarantineStatus) ([]QuarantineRecord, error) {
	rows, err := s.db.Query(`SELECT ` + quarantineCols + ` FROM quarantine ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: quarantine: %w", err)
	}
	defer func() { _ = rows.Close() }()
	want := map[QuarantineStatus]bool{}
	for _, st := range statuses {
		want[st] = true
	}
	out := []QuarantineRecord{}
	for rows.Next() {
		r, err := scanQuarantine(rows)
		if err != nil {
			return nil, fmt.Errorf("store: quarantine: %w", err)
		}
		if len(want) == 0 || want[r.Status] {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}
