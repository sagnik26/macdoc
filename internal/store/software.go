package store

import (
	"database/sql"
	"fmt"
	"time"
)

// SoftwareRow is one installed program as stored for a scan.
type SoftwareRow struct {
	ID        string
	Name      string
	Kind      string
	Version   string
	BundleID  string
	Source    string
	Installed time.Time // zero when unknown
}

// OwnerRow says a scanned folder belongs to a software row.
type OwnerRow struct {
	SoftwareID string
	Path       string
	Bytes      int64
	Confidence float64
	Evidence   string
}

// SourceRow is how one discovery source did in a scan.
type SourceRow struct {
	Source string
	State  string // "ok", "unknown" or "absent"
	Reason string
}

// Inventory is a scan's stored software snapshot.
type Inventory struct {
	Software []SoftwareRow
	Owners   []OwnerRow
	Sources  []SourceRow
}

// SaveInventory stores the software snapshot of one scan. It replaces any
// earlier snapshot of that scan, in one transaction.
func (s *Store) SaveInventory(scanID string, inv Inventory) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("store: software: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM software WHERE scan_id = ?`,
		`DELETE FROM software_owner WHERE scan_id = ?`,
		`DELETE FROM software_source WHERE scan_id = ?`,
	} {
		if _, err := tx.Exec(q, scanID); err != nil {
			return fmt.Errorf("store: software: %w", err)
		}
	}
	for _, r := range inv.Software {
		if _, err := tx.Exec(`INSERT INTO software (scan_id, id, name, kind, version, bundle_id, source, installed)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			scanID, r.ID, r.Name, r.Kind, r.Version, r.BundleID, r.Source, installedDB(r.Installed)); err != nil {
			return fmt.Errorf("store: software: %w", err)
		}
	}
	for _, o := range inv.Owners {
		if _, err := tx.Exec(`INSERT INTO software_owner (scan_id, path, software_id, bytes, confidence, evidence)
			VALUES (?, ?, ?, ?, ?, ?)`,
			scanID, o.Path, o.SoftwareID, o.Bytes, o.Confidence, o.Evidence); err != nil {
			return fmt.Errorf("store: software: %w", err)
		}
	}
	for _, r := range inv.Sources {
		if _, err := tx.Exec(`INSERT INTO software_source (scan_id, source, state, reason) VALUES (?, ?, ?, ?)`,
			scanID, r.Source, r.State, r.Reason); err != nil {
			return fmt.Errorf("store: software: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: software: %w", err)
	}
	return nil
}

// Inventory returns a scan's stored snapshot. A scan that has none returns an
// empty Inventory, not an error.
func (s *Store) Inventory(scanID string) (Inventory, error) {
	var inv Inventory
	rows, err := s.DB.Query(`SELECT id, name, kind, version, bundle_id, source, installed
		FROM software WHERE scan_id = ? ORDER BY id`, scanID)
	if err != nil {
		return inv, fmt.Errorf("store: software: %w", err)
	}
	for rows.Next() {
		var r SoftwareRow
		var installed int64
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.Version, &r.BundleID, &r.Source, &installed); err != nil {
			_ = rows.Close()
			return inv, fmt.Errorf("store: software: %w", err)
		}
		r.Installed = installedTime(installed)
		inv.Software = append(inv.Software, r)
	}
	if err := rows.Close(); err != nil {
		return inv, fmt.Errorf("store: software: %w", err)
	}
	owners, err := s.DB.Query(`SELECT software_id, path, bytes, confidence, evidence
		FROM software_owner WHERE scan_id = ? ORDER BY path`, scanID)
	if err != nil {
		return inv, fmt.Errorf("store: software: %w", err)
	}
	for owners.Next() {
		var o OwnerRow
		if err := owners.Scan(&o.SoftwareID, &o.Path, &o.Bytes, &o.Confidence, &o.Evidence); err != nil {
			_ = owners.Close()
			return inv, fmt.Errorf("store: software: %w", err)
		}
		inv.Owners = append(inv.Owners, o)
	}
	if err := owners.Close(); err != nil {
		return inv, fmt.Errorf("store: software: %w", err)
	}
	sources, err := s.DB.Query(`SELECT source, state, reason FROM software_source WHERE scan_id = ? ORDER BY source`, scanID)
	if err != nil {
		return inv, fmt.Errorf("store: software: %w", err)
	}
	for sources.Next() {
		var r SourceRow
		if err := sources.Scan(&r.Source, &r.State, &r.Reason); err != nil {
			_ = sources.Close()
			return inv, fmt.Errorf("store: software: %w", err)
		}
		inv.Sources = append(inv.Sources, r)
	}
	if err := sources.Close(); err != nil {
		return inv, fmt.Errorf("store: software: %w", err)
	}
	return inv, nil
}

// PreviousScanID returns the complete scan taken before scanID, if any.
func (s *Store) PreviousScanID(scanID string) (string, bool, error) {
	var id string
	err := s.DB.QueryRow(`SELECT p.id FROM scans p, scans c
		WHERE c.id = ? AND p.status = 'complete' AND p.id <> c.id AND p.started_at < c.started_at
		ORDER BY p.started_at DESC, p.rowid DESC LIMIT 1`, scanID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: software: %w", err)
	}
	return id, true, nil
}

// installedDB stores a time as unix nanoseconds, with 0 for unknown.
func installedDB(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func installedTime(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, v)
}
