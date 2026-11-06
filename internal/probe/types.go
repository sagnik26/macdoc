// Package probe reads hardware, memory, swap, disk and snapshot state. It is
// strictly read-only: every external command goes through platform.Runner.
//
// Parsers are pure functions over captured command output so they can be
// golden-tested; Collect* functions run the commands and call the parsers.
//
// CONTRACT (tech lead): the types and function signatures in this file are
// fixed. Implementation: P1-W1 (hardware, memory, swap, processes) and P1-W2
// (disk, APFS, snapshots, purgeable).
package probe

import "github.com/sagnik26/macdoc/internal/model"

// Hardware describes the machine and OS.
type Hardware struct {
	Model       string `json:"model"`        // hw.model, e.g. "MacBookAir10,1"
	Chip        string `json:"chip"`         // machdep.cpu.brand_string, e.g. "Apple M1"
	CPUCount    int    `json:"cpu_count"`    // hw.ncpu
	MemoryBytes int64  `json:"memory_bytes"` // hw.memsize
	OSVersion   string `json:"os_version"`   // sw_vers ProductVersion
	OSBuild     string `json:"os_build"`     // sw_vers BuildVersion
}

// Pressure is the kernel memory pressure level.
type Pressure string

// The memory pressure levels macOS reports.
const (
	PressureNormal   Pressure = "normal"
	PressureWarn     Pressure = "warn"
	PressureCritical Pressure = "critical"
	PressureUnknown  Pressure = "unknown"
)

// Memory is a snapshot of RAM usage. Byte counts are derived from vm_stat
// page counts times the reported page size.
type Memory struct {
	TotalBytes      int64    `json:"total_bytes"`
	FreeBytes       int64    `json:"free_bytes"`
	ActiveBytes     int64    `json:"active_bytes"`
	InactiveBytes   int64    `json:"inactive_bytes"`
	WiredBytes      int64    `json:"wired_bytes"`
	CompressedBytes int64    `json:"compressed_bytes"` // "occupied by compressor"
	FreePercent     int      `json:"free_percent"`     // memory_pressure "free percentage", -1 if unknown
	Pressure        Pressure `json:"pressure"`
}

// Swap is vm.swapusage.
type Swap struct {
	TotalBytes int64 `json:"total_bytes"`
	UsedBytes  int64 `json:"used_bytes"`
	FreeBytes  int64 `json:"free_bytes"`
	Encrypted  bool  `json:"encrypted"`
}

// ProcessGroup aggregates resident memory of related processes (e.g. all
// "Google Chrome Helper" processes under "Google Chrome").
type ProcessGroup struct {
	Name     string `json:"name"`
	RSSBytes int64  `json:"rss_bytes"`
	Count    int    `json:"count"`
}

// Volume is one APFS volume in the boot container.
type Volume struct {
	Name       string   `json:"name"`
	Roles      []string `json:"roles"` // e.g. ["System"], ["Data"], ["VM"]
	MountPoint string   `json:"mount_point,omitempty"`
	UsedBytes  int64    `json:"used_bytes"` // "capacity in use" (CapacityInUse)
}

// Disk describes the APFS container holding the boot volume.
type Disk struct {
	ContainerRef        string      `json:"container_ref"` // e.g. "disk3"
	ContainerTotalBytes int64       `json:"container_total_bytes"`
	ContainerFreeBytes  int64       `json:"container_free_bytes"` // not including purgeable
	PurgeableBytes      model.Bytes `json:"purgeable_bytes"`      // unknown unless the cgo shim is available
	Volumes             []Volume    `json:"volumes"`
}

// Snapshots lists local Time Machine snapshots on the boot Data volume.
type Snapshots struct {
	Count int      `json:"count"`
	Names []string `json:"names"` // e.g. "com.apple.TimeMachine.2026-10-27-101500.local"
}

// Report is everything `macdoc doctor` shows. A collector that fails leaves
// its section zero-valued and appends a human-readable warning; it never
// aborts the whole report.
type Report struct {
	Hardware     Hardware       `json:"hardware"`
	Memory       Memory         `json:"memory"`
	Swap         Swap           `json:"swap"`
	TopProcesses []ProcessGroup `json:"top_processes"`
	Disk         Disk           `json:"disk"`
	Snapshots    Snapshots      `json:"snapshots"`
	Warnings     []string       `json:"warnings,omitempty"`
}
