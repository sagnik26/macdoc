// Package audit is macdoc's append-only history of changes
// (docs/SAFETY_MODEL.md §4.7): one JSON record per line in audit.jsonl,
// synced to disk before Append returns. It never rewrites or truncates the
// file, and a torn last line (a crash mid-write) is skipped when reading.
package audit

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Event names.
const (
	Planned  = "plan_created"
	Started  = "apply_started"
	Skipped  = "item_skipped"
	Done     = "item_done"
	Failed   = "item_failed"
	Finished = "apply_finished"
	Restored = "quarantine_restored"
	Purged   = "quarantine_purged"
)

// Record is one audit line. Fields that don't apply to an event are omitted.
type Record struct {
	TS    time.Time `json:"ts"`
	Event string    `json:"event"`
	// Run identifies one macdoc invocation that wrote records (a plan, an
	// apply, a restore, a purge); records of one run share it.
	Run    string   `json:"run,omitempty"`
	Plan   string   `json:"plan,omitempty"`
	Origin string   `json:"origin,omitempty"`
	Item   string   `json:"item,omitempty"`
	Recipe string   `json:"recipe,omitempty"`
	Target string   `json:"target,omitempty"`
	Method string   `json:"method,omitempty"`
	Argv   []string `json:"argv,omitempty"`
	// Privileged is true when the command ran with administrator rights.
	Privileged bool `json:"privileged,omitempty"`
	// NonInteractive is true when the run was started with reclaim --yes: no
	// person confirmed it at the terminal (ADR 0014 §5).
	NonInteractive bool   `json:"non_interactive,omitempty"`
	Entry          string `json:"quarantine_id,omitempty"`
	Items          int    `json:"items,omitempty"`
	Expected       int64  `json:"expected_bytes,omitempty"`
	// Actual is the measured change in free space (apply_finished), or
	// the bytes moved or purged. Nil means it wasn't measured.
	Actual *int64 `json:"actual_bytes,omitempty"`
	Result string `json:"result,omitempty"` // ok | skipped | failed | partial
	Reason string `json:"reason,omitempty"`
	Exit   *int   `json:"exit_code,omitempty"`
	Output string `json:"output,omitempty"` // a native command's stdout and stderr, tail-truncated
}

// NewRunID returns a fresh run identifier such as "R-3fa9c01b".
func NewRunID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "R-" + hex.EncodeToString([]byte(time.Now().Format("0405")))
	}
	return "R-" + hex.EncodeToString(b[:])
}

// File is the audit log's name inside the state directory.
const File = "audit.jsonl"

// Path is the audit log next to the store.
func Path(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), File)
}

// MaxOutput bounds the command output kept per record.
const MaxOutput = 16 << 10

// Tail keeps the last MaxOutput bytes of b, marking a cut.
func Tail(b []byte) string {
	if len(b) <= MaxOutput {
		return string(b)
	}
	return "…" + string(b[len(b)-MaxOutput:])
}

// Probe checks that the audit log can be opened for writing, without writing
// anything. Actions that can't be undone (a purge, a restore) call it first,
// so nothing changes when the record of it couldn't be written (P6-R1 F7).
func Probe(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // G304: macdoc's own audit log.
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	return f.Close()
}

// Append writes rec as one line to the log at path and syncs it. The file is
// created 0600 if missing and opened append-only, under an exclusive lock so
// concurrent writers can't interleave lines.
func Append(path string, rec Record) error {
	if rec.TS.IsZero() {
		rec.TS = time.Now()
	}
	rec.TS = rec.TS.UTC().Round(time.Millisecond)
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // G304: macdoc's own audit log.
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: lock: %w", err)
	}
	// A previous crash may have left a torn line; start ours on a new one.
	prefix, err := newline(f)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: %w", err)
	}
	buf := make([]byte, 0, len(line)+2)
	if prefix {
		buf = append(buf, '\n')
	}
	buf = append(append(buf, line...), '\n')
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("audit: %w", err)
	}
	return f.Close()
}

// newline reports whether the file is non-empty and doesn't end in a
// newline.
func newline(f *os.File) (bool, error) {
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return false, err
	}
	b := make([]byte, 1)
	if _, err := f.ReadAt(b, fi.Size()-1); err != nil {
		return false, err
	}
	return b[0] != '\n', nil
}

// longest bounds one record when reading.
const longest = 1 << 20

// Read returns the log's records, oldest first, keeping only the last limit
// (all if limit <= 0). Lines that don't parse (a torn write) are counted in
// bad, not returned. A missing log is empty.
func Read(path string, limit int) (recs []Record, bad int, err error) {
	f, err := os.Open(path) //nolint:gosec // G304: macdoc's own audit log.
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("audit: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only
	return parse(f, limit)
}

func parse(r io.Reader, limit int) ([]Record, int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), longest)
	out := []Record{}
	bad := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil || rec.Event == "" {
			bad++
			continue
		}
		out = append(out, rec)
		if limit > 0 && len(out) > limit {
			out = out[1:]
		}
	}
	if err := sc.Err(); err != nil {
		return out, bad, fmt.Errorf("audit: %w", err)
	}
	return out, bad, nil
}
