// Package fsread holds the read-only file helpers shared by detectors and
// project discovery. Nothing here writes, renames or deletes.
package fsread

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// MaxSmallFile is the default limit for SmallFile.
const MaxSmallFile int64 = 64 << 10

// SmallFile errors: anything but a regular file, or over the limit.
var (
	ErrNotRegular = errors.New("not a regular file")
	ErrTooLarge   = errors.New("file too large")
)

// SmallFile reads a small regular file. It never follows a symlink in the
// final component, never blocks on a FIFO or device (it checks with Lstat,
// opens non-blocking and checks again), and fails if the file is larger than
// limit bytes.
func SmallFile(path string, limit int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0) //nolint:gosec // G304: read-only, regular files only (checked above and below).
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if fi, err = f.Stat(); err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: %w (over %d bytes)", path, ErrTooLarge, limit)
	}
	return b, nil
}

// IsRegular reports whether path is a regular file (a symlink doesn't count).
func IsRegular(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode().IsRegular()
}

// IsDir reports whether path is a directory (a symlink doesn't count).
func IsDir(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir()
}
