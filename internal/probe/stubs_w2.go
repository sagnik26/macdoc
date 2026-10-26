package probe

// CONTRACT for P1-W2 (disk, APFS, snapshots, purgeable). Delete this file
// and implement these exact signatures in your own files.

import (
	"context"

	"macdoc/internal/platform"
)

// ParseDiskutilInfo parses `diskutil info -plist /` and returns the APFS
// container reference (e.g. "disk3") plus container total and free bytes.
func ParseDiskutilInfo(plist []byte) (containerRef string, total, free int64, err error) {
	return "", 0, 0, ErrNotImplemented
}

// ParseAPFSList parses `diskutil apfs list -plist` and returns the volumes of
// the given container.
func ParseAPFSList(plist []byte, containerRef string) ([]Volume, error) {
	return nil, ErrNotImplemented
}

// ParseLocalSnapshots parses `tmutil listlocalsnapshots /`.
func ParseLocalSnapshots(out []byte) (Snapshots, error) { return Snapshots{}, ErrNotImplemented }

// CollectDisk runs the diskutil commands through r. PurgeableBytes comes from
// the optional cgo shim (internal/platform/darwin) and is SourceUnknown
// without it.
func CollectDisk(ctx context.Context, r platform.Runner) (Disk, error) {
	return Disk{}, ErrNotImplemented
}

// CollectSnapshots runs tmutil listlocalsnapshots / through r.
func CollectSnapshots(ctx context.Context, r platform.Runner) (Snapshots, error) {
	return Snapshots{}, ErrNotImplemented
}
