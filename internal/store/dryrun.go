package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DryRun is one stored dry-run result (migration 6): the command's key, the
// fingerprint of the tool's state it was taken under, when, and what it
// printed.
type DryRun struct {
	Key         string
	Fingerprint string
	At          time.Time
	ExitCode    int
	Stdout      []byte
	Stderr      []byte
}

// DryRun returns the stored result for key. The boolean is false when there
// is none.
func (s *Store) DryRun(key string) (DryRun, bool, error) {
	d := DryRun{Key: key}
	var at int64
	err := s.DB.QueryRow(`SELECT fingerprint, at, exit_code, stdout, stderr FROM dryruns WHERE key = ?`, key).
		Scan(&d.Fingerprint, &at, &d.ExitCode, &d.Stdout, &d.Stderr)
	if errors.Is(err, sql.ErrNoRows) {
		return DryRun{}, false, nil
	}
	if err != nil {
		return DryRun{}, false, fmt.Errorf("store: dry run: %w", err)
	}
	d.At = fromDB(at)
	return d, true, nil
}

// SaveDryRun stores d, replacing any earlier result for the same key.
func (s *Store) SaveDryRun(d DryRun) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	stdout, stderr := d.Stdout, d.Stderr
	if stdout == nil {
		stdout = []byte{}
	}
	if stderr == nil {
		stderr = []byte{}
	}
	if _, err := s.DB.Exec(`INSERT OR REPLACE INTO dryruns (key, fingerprint, at, exit_code, stdout, stderr)
		VALUES (?, ?, ?, ?, ?, ?)`, d.Key, d.Fingerprint, toDB(d.At), d.ExitCode, stdout, stderr); err != nil {
		return fmt.Errorf("store: dry run: %w", err)
	}
	return nil
}
