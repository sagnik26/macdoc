package probe

// CONTRACT for P1-W1 (hardware, memory, swap, processes). Delete this file
// and implement these exact signatures in your own files.

import (
	"context"

	"macdoc/internal/platform"
)

// ParseSysctl parses `sysctl key1 key2 ...` output ("key: value" lines).
func ParseSysctl(out []byte) (map[string]string, error) { return nil, ErrNotImplemented }

// ParseSwVers parses `sw_vers` output into version and build.
func ParseSwVers(out []byte) (version, build string, err error) { return "", "", ErrNotImplemented }

// ParseVMStat parses `vm_stat` output (page size from its header line).
// TotalBytes, FreePercent and Pressure are left for the collector to fill.
func ParseVMStat(out []byte) (Memory, error) { return Memory{}, ErrNotImplemented }

// ParseMemoryPressure parses `memory_pressure -Q` output: free percentage.
func ParseMemoryPressure(out []byte) (freePercent int, err error) { return 0, ErrNotImplemented }

// ParsePressureLevel maps `sysctl -n kern.memorystatus_vm_pressure_level`
// output (1 normal, 2 warn, 4 critical) to a Pressure; anything else is
// PressureUnknown.
func ParsePressureLevel(out []byte) Pressure { return PressureUnknown }

// ParseSwapUsage parses `sysctl -n vm.swapusage`.
func ParseSwapUsage(out []byte) (Swap, error) { return Swap{}, ErrNotImplemented }

// ParsePS parses `ps -axo rss=,comm=` output and returns the top n groups by
// resident memory.
func ParsePS(out []byte, n int) ([]ProcessGroup, error) { return nil, ErrNotImplemented }

// CollectHardware runs sysctl + sw_vers through r.
func CollectHardware(ctx context.Context, r platform.Runner) (Hardware, error) {
	return Hardware{}, ErrNotImplemented
}

// CollectMemory runs vm_stat, sysctl hw.memsize, memory_pressure -Q and the
// pressure-level sysctl through r. Partial failures fill what they can.
func CollectMemory(ctx context.Context, r platform.Runner) (Memory, error) {
	return Memory{}, ErrNotImplemented
}

// CollectSwap runs sysctl -n vm.swapusage through r.
func CollectSwap(ctx context.Context, r platform.Runner) (Swap, error) {
	return Swap{}, ErrNotImplemented
}

// CollectProcesses runs ps through r and returns the top n groups.
func CollectProcesses(ctx context.Context, r platform.Runner, n int) ([]ProcessGroup, error) {
	return nil, ErrNotImplemented
}
