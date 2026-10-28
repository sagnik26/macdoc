package store

import (
	"fmt"
	"time"
)

// maxVitals is how many samples are kept; older ones are dropped.
const maxVitals = 2000

// Vitals is one memory and disk sample. Sizes are bytes; -1 means unknown.
type Vitals struct {
	At          time.Time `json:"at"`
	MemTotal    int64     `json:"mem_total"`
	SwapUsed    int64     `json:"swap_used"`
	FreePercent int       `json:"free_percent"`
	Pressure    string    `json:"pressure"`
	DiskFree    int64     `json:"disk_free"`
}

// AddVitals records a sample and drops the oldest beyond maxVitals.
func (s *Store) AddVitals(v Vitals) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: vitals: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO vitals (at, mem_total, swap_used, free_percent, pressure, disk_free)
		VALUES (?, ?, ?, ?, ?, ?)`, toDB(v.At), v.MemTotal, v.SwapUsed, v.FreePercent, v.Pressure, v.DiskFree); err != nil {
		return fmt.Errorf("store: vitals: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM vitals WHERE rowid NOT IN
		(SELECT rowid FROM vitals ORDER BY at DESC, rowid DESC LIMIT ?)`, maxVitals); err != nil {
		return fmt.Errorf("store: vitals: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: vitals: %w", err)
	}
	return nil
}

// VitalsSince returns the samples taken at or after since, oldest first.
func (s *Store) VitalsSince(since time.Time) ([]Vitals, error) {
	rows, err := s.db.Query(`SELECT at, mem_total, swap_used, free_percent, pressure, disk_free
		FROM vitals WHERE at >= ? ORDER BY at, rowid`, toDB(since))
	if err != nil {
		return nil, fmt.Errorf("store: vitals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Vitals{}
	for rows.Next() {
		var (
			v  Vitals
			at int64
		)
		if err := rows.Scan(&at, &v.MemTotal, &v.SwapUsed, &v.FreePercent, &v.Pressure, &v.DiskFree); err != nil {
			return nil, fmt.Errorf("store: vitals: %w", err)
		}
		v.At = fromDB(at)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: vitals: %w", err)
	}
	return out, nil
}
