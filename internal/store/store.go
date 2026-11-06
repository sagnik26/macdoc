// Package store persists scans, nodes, unmeasured paths, plans, quarantine
// records and memory samples in a local SQLite database (pure-Go
// modernc.org/sqlite, WAL mode).
//
// A scan is derived data and can be rebuilt by scanning again. A plan record
// holds the second copy of a plan's seal and a quarantine record is what
// makes a folder in quarantine one that macdoc created, so neither can be
// rebuilt.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/sagnik26/macdoc/internal/model"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// ErrNoScan is returned when no scan matches a query.
var ErrNoScan = errors.New("store: no scan")

// DefaultPath returns ~/Library/Application Support/macdoc/state.db for home.
func DefaultPath(home string) string {
	return home + "/Library/Application Support/macdoc/state.db"
}

const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600

	// maxOpenConns bounds the pool. WAL lets readers run alongside the single
	// writer; in-process writers are serialized by Store.writeMu, so the
	// SQLite write lock is only ever contended by other processes, which
	// busy_timeout covers.
	maxOpenConns = 4

	idAttempts = 8
)

// Store is an open database. It is safe for use by one process; methods are
// safe for concurrent use by multiple goroutines.
type Store struct {
	DB      *sql.DB
	writeMu sync.Mutex // serializes write transactions within the process
}

// Open opens (creating if needed, with parent dirs 0700 and the file 0600)
// the database at path and applies Migrations.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, fmt.Errorf("store: create directory: %w", err)
	}
	if err := ensureFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// ensureFile creates the database file with mode 0600, or tightens the mode
// of an existing file whose permissions are looser.
func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode) // #nosec G304 -- the caller chooses the state db path
	if err != nil {
		return fmt.Errorf("store: create database file: %w", err)
	}
	fi, err := f.Stat()
	if err == nil && fi.Mode().Perm()&^fileMode != 0 {
		err = f.Chmod(fi.Mode().Perm() & fileMode)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("store: database file mode: %w", err)
	}
	return nil
}

// dsn builds the connection string. Pragmas are applied on every new pooled
// connection (foreign_keys and busy_timeout are per-connection settings), and
// write transactions take the lock up front (BEGIN IMMEDIATE) so they never
// fail on a read-to-write lock upgrade.
func dsn(path string) string {
	q := url.Values{}
	for _, p := range []string{
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"foreign_keys(ON)",
		"synchronous(NORMAL)",
	} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	u := url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: q.Encode()}
	return u.String()
}

// Close closes the database.
func (s *Store) Close() error {
	return s.DB.Close()
}

// BeginScan records a new running scan and returns its ID ("S-" + 8 base32
// chars, unique).
func (s *Store) BeginScan(root, macdocVersion string, startedAt time.Time) (string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for range idAttempts {
		id, err := newScanID()
		if err != nil {
			return "", err
		}
		res, err := s.DB.Exec(`INSERT INTO scans
			(id, root, started_at, status, total_bytes, total_files, unmeasured_count, macdoc_version)
			VALUES (?, ?, ?, ?, 0, 0, 0, ?) ON CONFLICT(id) DO NOTHING`,
			id, root, toDB(startedAt), string(model.ScanRunning), macdocVersion)
		if err != nil {
			return "", fmt.Errorf("store: begin scan: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return "", fmt.Errorf("store: begin scan: %w", err)
		} else if n == 1 {
			return id, nil
		}
	}
	return "", fmt.Errorf("store: begin scan: no unique ID after %d attempts", idAttempts)
}

// RandRead is crypto/rand.Read; tests replace it to force ID collisions.
var RandRead = rand.Read

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newScanID returns "S-" plus 8 base32 characters (40 random bits).
func newScanID() (string, error) {
	var b [5]byte
	if _, err := RandRead(b[:]); err != nil {
		return "", fmt.Errorf("store: scan ID: %w", err)
	}
	return "S-" + idEncoding.EncodeToString(b[:]), nil
}

// AddNodes stores kept nodes for a running scan in one transaction. It may
// be called repeatedly (batches).
func (s *Store) AddNodes(scanID string, nodes []model.Node) error {
	return s.writeRunning(scanID, func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT INTO nodes
			(scan_id, path, path_fold, kind, bytes, files, mtime_max, depth, rel_depth)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(scan_id, path_fold) DO UPDATE SET
				path = excluded.path, kind = excluded.kind, bytes = excluded.bytes,
				files = excluded.files, mtime_max = excluded.mtime_max,
				depth = excluded.depth, rel_depth = excluded.rel_depth`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, n := range nodes {
			if _, err := stmt.Exec(scanID, n.Path, Fold(n.Path), string(n.Kind), n.Bytes,
				n.Files, n.MtimeMax, DepthOf(n.Path), n.Depth); err != nil {
				return fmt.Errorf("node %q: %w", n.Path, err)
			}
		}
		return nil
	})
}

// AddUnmeasured stores unreadable paths for a running scan.
func (s *Store) AddUnmeasured(scanID string, items []model.Unmeasured) error {
	return s.writeRunning(scanID, func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT INTO unmeasured (scan_id, path, reason) VALUES (?, ?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, u := range items {
			if _, err := stmt.Exec(scanID, u.Path, u.Reason); err != nil {
				return fmt.Errorf("unmeasured %q: %w", u.Path, err)
			}
		}
		return nil
	})
}

// FinishScan marks a scan complete or incomplete and records its totals.
// Finishing a scan that is not running is an error.
func (s *Store) FinishScan(scanID string, status model.ScanStatus, totalBytes, totalFiles int64, finishedAt time.Time) error {
	if status != model.ScanComplete && status != model.ScanIncomplete {
		return fmt.Errorf("store: finish scan %s: invalid status %q", scanID, status)
	}
	return s.writeRunning(scanID, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE scans SET status = ?, total_bytes = ?, total_files = ?,
				finished_at = ?,
				unmeasured_count = (SELECT COUNT(*) FROM unmeasured WHERE scan_id = ?)
			WHERE id = ?`,
			string(status), totalBytes, totalFiles, toDB(finishedAt), scanID, scanID)
		return err
	})
}

// writeRunning runs fn in a write transaction after checking that scanID
// exists and is running.
func (s *Store) writeRunning(scanID string, fn func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return fmt.Errorf("store: %s: %w", scanID, err)
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	err = tx.QueryRow(`SELECT status FROM scans WHERE id = ?`, scanID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: %s: %w", scanID, ErrNoScan)
	}
	if err != nil {
		return fmt.Errorf("store: %s: %w", scanID, err)
	}
	if model.ScanStatus(status) != model.ScanRunning {
		return fmt.Errorf("store: scan %s is %s, not running", scanID, status)
	}
	if err := fn(tx); err != nil {
		return fmt.Errorf("store: %s: %w", scanID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %s: commit: %w", scanID, err)
	}
	return nil
}

const scanCols = `id, root, started_at, finished_at, status, total_bytes, total_files,
	unmeasured_count, macdoc_version`

func scanInfo(row *sql.Row) (model.ScanInfo, error) {
	var (
		si       model.ScanInfo
		started  int64
		finished sql.NullInt64
		status   string
	)
	err := row.Scan(&si.ID, &si.Root, &started, &finished, &status, &si.TotalBytes,
		&si.TotalFiles, &si.UnmeasuredCount, &si.MacdocVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ScanInfo{}, ErrNoScan
	}
	if err != nil {
		return model.ScanInfo{}, fmt.Errorf("store: read scan: %w", err)
	}
	si.Status = model.ScanStatus(status)
	si.StartedAt = fromDB(started)
	if finished.Valid {
		si.FinishedAt = fromDB(finished.Int64)
	}
	return si, nil
}

// Scan returns one scan's info.
func (s *Store) Scan(scanID string) (model.ScanInfo, error) {
	return scanInfo(s.DB.QueryRow(`SELECT `+scanCols+` FROM scans WHERE id = ?`, scanID))
}

// LatestScan returns the most recently started scan with the given status
// (any status if status is ""), or ErrNoScan.
func (s *Store) LatestScan(status model.ScanStatus) (model.ScanInfo, error) {
	return scanInfo(s.DB.QueryRow(`SELECT `+scanCols+` FROM scans
		WHERE ? = '' OR status = ?
		ORDER BY started_at DESC, rowid DESC LIMIT 1`, string(status), string(status)))
}

// ScanAtOrBefore returns the newest complete scan started at or before at,
// or ErrNoScan.
func (s *Store) ScanAtOrBefore(at time.Time) (model.ScanInfo, error) {
	return scanInfo(s.DB.QueryRow(`SELECT `+scanCols+` FROM scans
		WHERE status = ? AND started_at <= ?
		ORDER BY started_at DESC, rowid DESC LIMIT 1`, string(model.ScanComplete), toDB(at)))
}

// OldestScan returns the oldest complete scan, or ErrNoScan.
func (s *Store) OldestScan() (model.ScanInfo, error) {
	return scanInfo(s.DB.QueryRow(`SELECT `+scanCols+` FROM scans
		WHERE status = ? ORDER BY started_at, rowid LIMIT 1`, string(model.ScanComplete)))
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
	if err := s.scanExists(scanID); err != nil {
		return nil, err
	}
	// Unused filters are disabled through their parameters (an empty prefix,
	// maxDepth -1, LIMIT -1) so the SQL text stays fixed.
	prefix, like, prefixDepth := "", "", 0
	if q.Prefix != "" {
		if p := Fold(path.Clean(q.Prefix)); p != "/" {
			prefix, like, prefixDepth = p, EscapeLike(p)+"/%", DepthOf(p)
		}
	}
	maxDepth := -1
	if q.MaxDepth > 0 {
		maxDepth = prefixDepth + q.MaxDepth
	}
	limit := -1
	if q.Limit > 0 {
		limit = q.Limit
	}
	rows, err := s.DB.Query(`SELECT path, kind, bytes, files, mtime_max, rel_depth FROM nodes
		WHERE scan_id = ? AND bytes >= ?
			AND (? = '' OR path_fold = ? OR path_fold LIKE ? ESCAPE '\')
			AND (? < 0 OR depth <= ?)
		ORDER BY bytes DESC, path ASC LIMIT ?`,
		scanID, q.MinBytes, prefix, prefix, like, maxDepth, maxDepth, limit)
	if err != nil {
		return nil, fmt.Errorf("store: nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []model.Node{}
	for rows.Next() {
		var (
			n    model.Node
			kind string
		)
		if err := rows.Scan(&n.Path, &kind, &n.Bytes, &n.Files, &n.MtimeMax, &n.Depth); err != nil {
			return nil, fmt.Errorf("store: nodes: %w", err)
		}
		n.Kind = model.NodeKind(kind)
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: nodes: %w", err)
	}
	return out, nil
}

// Unmeasured returns a scan's unreadable paths, sorted by path.
func (s *Store) Unmeasured(scanID string) ([]model.Unmeasured, error) {
	if err := s.scanExists(scanID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(`SELECT path, reason FROM unmeasured WHERE scan_id = ?
		ORDER BY path, rowid`, scanID)
	if err != nil {
		return nil, fmt.Errorf("store: unmeasured: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []model.Unmeasured{}
	for rows.Next() {
		var u model.Unmeasured
		if err := rows.Scan(&u.Path, &u.Reason); err != nil {
			return nil, fmt.Errorf("store: unmeasured: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: unmeasured: %w", err)
	}
	return out, nil
}

func (s *Store) scanExists(scanID string) error {
	var one int
	err := s.DB.QueryRow(`SELECT 1 FROM scans WHERE id = ?`, scanID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: %s: %w", scanID, ErrNoScan)
	}
	if err != nil {
		return fmt.Errorf("store: %s: %w", scanID, err)
	}
	return nil
}

// History returns (scanID, startedAt, bytes) for the given exact node path
// across complete scans, oldest first. Used by `trend` later.
func (s *Store) History(p string, limit int) ([]HistoryPoint, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.DB.Query(`SELECT s.id, s.started_at, n.bytes
		FROM nodes n JOIN scans s ON s.id = n.scan_id
		WHERE n.path_fold = ? AND s.status = ?
		ORDER BY s.started_at DESC, s.rowid DESC LIMIT ?`,
		Fold(path.Clean(p)), string(model.ScanComplete), limit)
	if err != nil {
		return nil, fmt.Errorf("store: history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []HistoryPoint{}
	for rows.Next() {
		var (
			h       HistoryPoint
			started int64
		)
		if err := rows.Scan(&h.ScanID, &started, &h.Bytes); err != nil {
			return nil, fmt.Errorf("store: history: %w", err)
		}
		h.StartedAt = fromDB(started)
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: history: %w", err)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
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
func (s *Store) PruneScans(keep int) error {
	return s.PruneScansWeekly(keep, 0, time.Time{})
}

// Week is the bucket PruneScansWeekly keeps one scan per.
const Week = 7 * 24 * time.Hour

// PruneScansWeekly is PruneScans that also keeps the newest complete scan of
// each calendar Week (weeks since the Unix epoch, so the buckets don't move)
// for the last weeks weeks before now, so `trend` has history to compare.
func (s *Store) PruneScansWeekly(keep, weeks int, now time.Time) error {
	if keep < 0 || weeks < 0 {
		return fmt.Errorf("store: prune: negative keep %d or weeks %d", keep, weeks)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.prune(keep, weeks, now); err != nil {
		return fmt.Errorf("store: prune: %w", err)
	}
	if _, err := s.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("store: prune: checkpoint: %w", err)
	}
	return nil
}

func (s *Store) prune(keep, weeks int, now time.Time) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	complete := string(model.ScanComplete)
	// The newest complete scan is read before anything is deleted, so keep=0
	// still uses it as the cut-off for incomplete scans.
	var newest sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(started_at) FROM scans WHERE status = ?`,
		complete).Scan(&newest); err != nil {
		return err
	}
	since := int64(math.MaxInt64) // no weekly scans unless weeks > 0
	if weeks > 0 {
		since = toDB(now.Add(-time.Duration(weeks) * Week))
	}
	if _, err := tx.Exec(`DELETE FROM scans WHERE status = ? AND id NOT IN
		(SELECT id FROM scans WHERE status = ? ORDER BY started_at DESC, rowid DESC LIMIT ?)
		AND id NOT IN (SELECT id FROM (SELECT id, ROW_NUMBER() OVER
			(PARTITION BY started_at / ? ORDER BY started_at DESC, rowid DESC) AS n
			FROM scans WHERE status = ? AND started_at >= ?) WHERE n = 1)`,
		complete, complete, keep, int64(Week), complete, since); err != nil {
		return err
	}
	if newest.Valid {
		if _, err := tx.Exec(`DELETE FROM scans WHERE status = ? AND started_at < ?`,
			string(model.ScanIncomplete), newest.Int64); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Fold is the case- and normalization-insensitive form of a path: NFC, then
// lower case.
func Fold(p string) string {
	return strings.ToLower(norm.NFC.String(p))
}

// DepthOf counts the non-empty components of p; "/" has depth 0.
func DepthOf(p string) int {
	n := 0
	for c := range strings.SplitSeq(p, "/") {
		if c != "" {
			n++
		}
	}
	return n
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike escapes LIKE wildcards and the escape character itself.
func EscapeLike(s string) string { return likeEscaper.Replace(s) }

// Times are stored as unix nanoseconds (INTEGER) and read back as UTC.
func toDB(t time.Time) int64 { return t.UnixNano() }

func fromDB(n int64) time.Time { return time.Unix(0, n).UTC() }
