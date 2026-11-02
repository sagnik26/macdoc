package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// LockPlans takes an exclusive lock that serializes plan creation across
// processes sharing the store at path (each MCP server is its own process).
// It blocks until the lock is free; call the returned function to release
// it. The lock file, plans.lock next to the database, is left in place.
func LockPlans(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, fmt.Errorf("store: plan lock: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(path), "plans.lock"), os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("store: plan lock: %w", err)
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("store: plan lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
