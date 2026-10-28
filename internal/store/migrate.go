package store

import (
	"fmt"
	"time"
)

// migrations are applied in order; migrations[i] is schema version i+1.
// Never edit a released migration: append a new one.
var migrations = []string{
	// v1: scans, kept nodes and unmeasured paths. Times are unix nanoseconds.
	// nodes.depth is the absolute component count of path ("/" = 0) and is
	// what NodeQuery.MaxDepth filters on; nodes.rel_depth is model.Node.Depth
	// as the scanner reported it (components below the scan root).
	`CREATE TABLE scans (
		id               TEXT PRIMARY KEY,
		root             TEXT NOT NULL,
		started_at       INTEGER NOT NULL,
		finished_at      INTEGER,
		status           TEXT NOT NULL CHECK (status IN ('running', 'complete', 'incomplete')),
		total_bytes      INTEGER NOT NULL DEFAULT 0,
		total_files      INTEGER NOT NULL DEFAULT 0,
		unmeasured_count INTEGER NOT NULL DEFAULT 0,
		macdoc_version   TEXT NOT NULL
	);
	CREATE INDEX scans_started_at ON scans (started_at);
	CREATE TABLE nodes (
		scan_id   TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
		path      TEXT NOT NULL,
		path_fold TEXT NOT NULL,
		kind      TEXT NOT NULL,
		bytes     INTEGER NOT NULL,
		files     INTEGER NOT NULL,
		mtime_max INTEGER NOT NULL,
		depth     INTEGER NOT NULL,
		rel_depth INTEGER NOT NULL,
		PRIMARY KEY (scan_id, path_fold)
	);
	CREATE INDEX nodes_scan_bytes ON nodes (scan_id, bytes DESC);
	CREATE TABLE unmeasured (
		scan_id TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
		path    TEXT NOT NULL,
		reason  TEXT NOT NULL
	);
	CREATE INDEX unmeasured_scan ON unmeasured (scan_id, path);`,
	// v2: sealed cleanup plans (Phase 5). The plan file holds the content;
	// this row is the second copy of its hash, so editing the file alone
	// can't produce a plan that verifies. Plans outlive scan pruning.
	`CREATE TABLE plans (
		id          TEXT PRIMARY KEY,
		sha256      TEXT NOT NULL,
		scan_id     TEXT NOT NULL,
		origin      TEXT NOT NULL CHECK (origin IN ('cli', 'claude')),
		created_at  INTEGER NOT NULL,
		expires_at  INTEGER NOT NULL,
		items       INTEGER NOT NULL,
		total_bytes INTEGER NOT NULL,
		status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'applied'))
	);
	CREATE INDEX plans_created_at ON plans (created_at);`,
	// v3: quarantine entries (Phase 6). The entry folder holds the manifest
	// and the moved payload; this row is macdoc's own record of it, so
	// restore and purge act only on folders macdoc created.
	`CREATE TABLE quarantine (
		id          TEXT PRIMARY KEY,
		plan_id     TEXT NOT NULL,
		item_id     TEXT NOT NULL,
		recipe_id   TEXT NOT NULL,
		original    TEXT NOT NULL,
		bytes       INTEGER NOT NULL,
		created_at  INTEGER NOT NULL,
		expires_at  INTEGER NOT NULL,
		status      TEXT NOT NULL CHECK (status IN ('pending', 'done', 'canceled', 'restored', 'purged'))
	);
	CREATE INDEX quarantine_created_at ON quarantine (created_at);`,
}

// migrate brings the schema up to len(migrations). Each migration runs in its
// own transaction together with its schema_migrations row. A database whose
// schema is newer than this code is rejected rather than downgraded.
func (s *Store) migrate() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	for {
		done, err := s.migrateOne()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

// migrateOne applies the next pending migration, or reports done. The
// version is read inside the (immediate) transaction, so two processes
// opening the same database cannot apply a migration twice.
func (s *Store) migrateOne() (done bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("store: migrate: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var cur int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&cur); err != nil {
		return false, fmt.Errorf("store: migrate: %w", err)
	}
	if cur > len(migrations) {
		return false, fmt.Errorf("store: database schema version %d is newer than this macdoc supports (%d)",
			cur, len(migrations))
	}
	if cur == len(migrations) {
		return true, nil
	}
	next := cur + 1
	if _, err := tx.Exec(migrations[cur]); err != nil {
		return false, fmt.Errorf("store: migration %d: %w", next, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		next, toDB(time.Now())); err != nil {
		return false, fmt.Errorf("store: migration %d: %w", next, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: migration %d: %w", next, err)
	}
	return false, nil
}
