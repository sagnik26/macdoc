// Package store persists scans, nodes, unmeasured paths and (later) plans in a
// local SQLite database (pure-Go modernc.org/sqlite, WAL mode).
//
// CONTRACT (tech lead): exported names and documented behavior are fixed.
// Implementation: task P2-W2 (docs/tasks/P2-W2.md).
package store

import (
	"errors"
	"time"

	"macdoc/internal/model"
)

// ErrNotImplemented is returned until P2-W2 lands.
var ErrNotImplemented = errors.New("store: not implemented")

// ErrNoScan is returned when no scan matches a query.
var ErrNoScan = errors.New("store: no scan")

// DefaultPath returns ~/Library/Application Support/macdoc/state.db for home.
func DefaultPath(home string) string {
	return home + "/Library/Application Support/macdoc/state.db"
}

// Store is an open database. It is safe for use by one process; methods are
// safe for concurrent use by multiple goroutines.
type Store struct {
	_ struct{} // implementation fields added by P2-W2
}

// Open opens (creating if needed, with parent dirs 0700 and the file 0600)
// the database at path and applies migrations.
func Open(path string) (*Store, error) { return nil, ErrNotImplemented }

// Close closes the database.
func (s *Store) Close() error { return ErrNotImplemented }

// BeginScan records a new running scan and returns its ID ("S-" + 8 base32
// chars, unique).
func (s *Store) BeginScan(root, macdocVersion string, startedAt time.Time) (string, error) {
	return "", ErrNotImplemented
}

// AddNodes stores kept nodes for a running scan in one transaction. It may
// be called repeatedly (batches).
func (s *Store) AddNodes(scanID string, nodes []model.Node) error { return ErrNotImplemented }

// AddUnmeasured stores unreadable paths for a running scan.
func (s *Store) AddUnmeasured(scanID string, items []model.Unmeasured) error {
	return ErrNotImplemented
}

// FinishScan marks a scan complete or incomplete and records its totals.
// Finishing a scan that is not running is an error.
func (s *Store) FinishScan(scanID string, status model.ScanStatus, totalBytes, totalFiles int64, finishedAt time.Time) error {
	return ErrNotImplemented
}

// Scan returns one scan's info.
func (s *Store) Scan(scanID string) (model.ScanInfo, error) {
	return model.ScanInfo{}, ErrNotImplemented
}

// LatestScan returns the most recently started scan with the given status
// (any status if status is ""), or ErrNoScan.
func (s *Store) LatestScan(status model.ScanStatus) (model.ScanInfo, error) {
	return model.ScanInfo{}, ErrNotImplemented
}

// NodeQuery filters Nodes.
type NodeQuery struct {
	Prefix   string // only nodes equal to or below this path ("" = all)
	MinBytes int64
	MaxDepth int // relative to Prefix's depth; 0 = unlimited
	Limit    int // 0 = unlimited
}

// Nodes returns a scan's kept nodes, Bytes descending (Path ascending on
// ties). Prefix matching is component-wise and case-insensitive.
func (s *Store) Nodes(scanID string, q NodeQuery) ([]model.Node, error) {
	return nil, ErrNotImplemented
}

// Unmeasured returns a scan's unreadable paths, sorted by path.
func (s *Store) Unmeasured(scanID string) ([]model.Unmeasured, error) {
	return nil, ErrNotImplemented
}

// History returns (scanID, startedAt, bytes) for the given exact node path
// across complete scans, oldest first. Used by `trend` later.
func (s *Store) History(path string, limit int) ([]HistoryPoint, error) {
	return nil, ErrNotImplemented
}

// HistoryPoint is one sample of a path's size over time.
type HistoryPoint struct {
	ScanID    string    `json:"scan_id"`
	StartedAt time.Time `json:"started_at"`
	Bytes     int64     `json:"bytes"`
}

// PruneScans deletes all but the newest keep complete scans (and every
// non-running incomplete scan older than the newest complete one), with their
// nodes and unmeasured rows.
func (s *Store) PruneScans(keep int) error { return ErrNotImplemented }
