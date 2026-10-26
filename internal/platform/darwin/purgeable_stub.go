//go:build !darwin || !cgo

// Package darwin holds optional macOS API shims that need cgo. It never runs
// external commands and never changes anything.
package darwin

// PurgeableBytes always reports unknown without cgo on darwin.
func PurgeableBytes() (int64, bool) { return 0, false }
