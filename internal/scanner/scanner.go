// Package scanner walks the Data volume and measures allocated space with
// bounded memory, at background priority. It is strictly read-only.
//
// CONTRACT (tech lead): exported names and documented behavior are fixed.
// Implementation: task P2-W1 (docs/tasks/P2-W1.md).
package scanner

import (
	"context"
	"errors"
	"time"

	"macdoc/internal/model"
)

// ErrNotImplemented is returned until P2-W1 lands.
var ErrNotImplemented = errors.New("scanner: not implemented")

// DataVolume is the default scan root on macOS.
const DataVolume = "/System/Volumes/Data"

// Options configures a scan.
type Options struct {
	// Root is the directory to scan (default DataVolume). The scan never
	// crosses onto another device (st_dev) and never follows symlinks.
	Root string
	// MapPrefix, if non-empty, is stripped from every reported path and
	// replaced by "/" (firmlink mapping: with Root=/System/Volumes/Data and
	// MapPrefix=/System/Volumes/Data, ".../Data/Users/x" is reported as
	// "/Users/x"). Default: equal to Root when Root == DataVolume, else "".
	MapPrefix string
	// Skip lists absolute (unmapped) paths that are not descended or counted
	// at all (e.g. .Spotlight-V100, .fseventsd, the quarantine root).
	Skip []string
	// Opaque lists absolute (unmapped) paths that are fully measured but
	// whose descendants are never kept (e.g. /private/var/folders).
	Opaque []string
	// KeepThreshold keeps any directory or file with at least this many
	// allocated bytes (default 100 MB).
	KeepThreshold int64
	// Keep, if non-nil, additionally keeps any directory for which it
	// returns true (mapped path), regardless of size, e.g. recipe roots and
	// project roots. It must be cheap and must not touch the filesystem.
	Keep func(path string) bool
	// Workers is the number of concurrent directory readers (default 4,
	// clamped to 1..16).
	Workers int
	// HardlinkLimit caps the number of (dev, ino) pairs tracked for
	// hard-link de-duplication (default 1,000,000). Beyond it, further hard
	// links are counted every time and Result.HardlinkLimitHit is set.
	HardlinkLimit int
	// Progress, if non-nil, is called at most every ProgressEvery.
	Progress      func(Progress)
	ProgressEvery time.Duration // default 250ms
}

// Progress is a periodic scan update.
type Progress struct {
	Files      int64
	Bytes      int64
	CurrentDir string // mapped path
	Elapsed    time.Duration
}

// Result is the outcome of a scan. Nodes are sorted by Bytes descending.
type Result struct {
	Root             string             // mapped root, e.g. "/"
	TotalBytes       int64              // allocated bytes under Root (excl. Skip)
	TotalFiles       int64              //
	Nodes            []model.Node       // kept nodes (always includes the root)
	Unmeasured       []model.Unmeasured // unreadable paths, mapped
	HardlinkLimitHit bool               //
	Incomplete       bool               // ctx was canceled; totals are partial
	Duration         time.Duration      //
}

// Scan walks opts.Root. It returns an error only for an unusable root;
// unreadable subdirectories become Unmeasured entries. On ctx cancellation it
// returns the partial Result with Incomplete set and a nil error.
func Scan(ctx context.Context, opts Options) (Result, error) {
	return Result{}, ErrNotImplemented
}

// SetBackgroundPriority lowers the current process to the macOS background
// band (CPU and I/O throttled) via setpriority(PRIO_DARWIN_PROCESS, 0,
// PRIO_DARWIN_BG). No exec, no cgo. Returns an error if the call fails.
func SetBackgroundPriority() error {
	return ErrNotImplemented
}
