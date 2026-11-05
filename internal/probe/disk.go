package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"howett.net/plist"

	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/platform/darwin"
)

// DiskutilTimeout bounds each diskutil invocation.
const DiskutilTimeout = 15 * time.Second

// purgeableReason explains why PurgeableBytes is unknown.
const purgeableReason = "purgeable space needs the cgo build"

// PurgeableLookup is the shim lookup; a variable so tests can replace it.
var PurgeableLookup = darwin.PurgeableBytes

var (
	cmdDiskutilInfo = platform.Command{Name: "diskutil", Args: []string{"info", "-plist", "/"}, Timeout: DiskutilTimeout}
	cmdAPFSList     = platform.Command{Name: "diskutil", Args: []string{"apfs", "list", "-plist"}, Timeout: DiskutilTimeout}
)

// diskutilInfo holds the keys of `diskutil info -plist` that we read.
// Pointers distinguish an absent key from a zero value.
type diskutilInfo struct {
	ContainerRef  string `plist:"APFSContainerReference"`
	ContainerSize *int64 `plist:"APFSContainerSize"`
	ContainerFree *int64 `plist:"APFSContainerFree"`
	TotalSize     *int64 `plist:"TotalSize"`
	FreeSpace     *int64 `plist:"FreeSpace"`
}

type apfsList struct {
	Containers []apfsContainer `plist:"Containers"`
}

type apfsContainer struct {
	ContainerReference string       `plist:"ContainerReference"`
	Volumes            []apfsVolume `plist:"Volumes"`
}

type apfsVolume struct {
	Name          string   `plist:"Name"`
	Roles         []string `plist:"Roles"`
	CapacityInUse int64    `plist:"CapacityInUse"`
	MountPoint    string   `plist:"MountPoint"`
}

// ParseDiskutilInfo parses `diskutil info -plist /` and returns the APFS
// container reference (e.g. "disk3") plus container total and free bytes.
// Total is APFSContainerSize (falling back to TotalSize) and free is
// APFSContainerFree (falling back to FreeSpace). A volume without an APFS
// container reference is an error.
func ParseDiskutilInfo(data []byte) (containerRef string, total, free int64, err error) {
	var info diskutilInfo
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return "", 0, 0, fmt.Errorf("parse diskutil info plist: %w", err)
	}
	if info.ContainerRef == "" {
		return "", 0, 0, errors.New("parse diskutil info plist: no APFS container reference (not an APFS volume?)")
	}
	return info.ContainerRef, firstSet(info.ContainerSize, info.TotalSize), firstSet(info.ContainerFree, info.FreeSpace), nil
}

// firstSet returns the first non-nil value, or 0.
func firstSet(vals ...*int64) int64 {
	for _, v := range vals {
		if v != nil {
			return *v
		}
	}
	return 0
}

// ParseAPFSList parses `diskutil apfs list -plist` and returns the volumes of
// the given container, sorted by UsedBytes descending. MountPoint is filled
// only when the plist has it.
func ParseAPFSList(data []byte, containerRef string) ([]Volume, error) {
	var list apfsList
	if _, err := plist.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse apfs list plist: %w", err)
	}
	for _, c := range list.Containers {
		if c.ContainerReference != containerRef {
			continue
		}
		vols := make([]Volume, 0, len(c.Volumes))
		for _, v := range c.Volumes {
			roles := v.Roles
			if roles == nil {
				roles = []string{}
			}
			vols = append(vols, Volume{Name: v.Name, Roles: roles, MountPoint: v.MountPoint, UsedBytes: v.CapacityInUse})
		}
		sort.SliceStable(vols, func(i, j int) bool { return vols[i].UsedBytes > vols[j].UsedBytes })
		return vols, nil
	}
	return nil, fmt.Errorf("parse apfs list plist: container %q not found", containerRef)
}

// CollectDisk runs the diskutil commands through r. PurgeableBytes comes from
// the optional cgo shim (internal/platform/darwin) and is SourceUnknown
// without it.
//
// Partial results: if `diskutil info` fails, CollectDisk returns a zero Disk
// and the error. If only `diskutil apfs list` fails, the returned Disk still
// carries the container reference, sizes and PurgeableBytes (Volumes is nil),
// together with a non-nil error the caller should show as a warning.
func CollectDisk(ctx context.Context, r platform.Runner) (Disk, error) {
	out, err := runRead(ctx, r, cmdDiskutilInfo)
	if err != nil {
		return Disk{}, err
	}
	ref, total, free, err := ParseDiskutilInfo(out)
	if err != nil {
		return Disk{}, err
	}
	d := Disk{ContainerRef: ref, ContainerTotalBytes: total, ContainerFreeBytes: free, PurgeableBytes: purgeable()}

	out, err = runRead(ctx, r, cmdAPFSList)
	if err != nil {
		return d, fmt.Errorf("disk volumes unavailable: %w", err)
	}
	vols, err := ParseAPFSList(out, ref)
	if err != nil {
		return d, fmt.Errorf("disk volumes unavailable: %w", err)
	}
	d.Volumes = vols
	return d, nil
}

func purgeable() model.Bytes {
	if n, ok := PurgeableLookup(); ok {
		return model.Bytes{Value: n, Source: model.SourceMeasured}
	}
	return model.Bytes{Source: model.SourceUnknown, Reason: purgeableReason}
}

// runRead runs a read-only command and returns stdout. A run error or a
// non-zero exit becomes an error naming the command, with trimmed stderr.
func runRead(ctx context.Context, r platform.Runner, cmd platform.Command) ([]byte, error) {
	res, err := r.Run(ctx, cmd)
	if err != nil {
		return nil, fmt.Errorf("run %s: %w", platform.Key(cmd), err)
	}
	if res.ExitCode != 0 {
		if msg := bytes.TrimSpace(res.Stderr); len(msg) > 0 {
			return nil, fmt.Errorf("run %s: exit status %d: %s", platform.Key(cmd), res.ExitCode, msg)
		}
		return nil, fmt.Errorf("run %s: exit status %d", platform.Key(cmd), res.ExitCode)
	}
	return res.Stdout, nil
}
