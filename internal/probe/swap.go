package probe

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"macdoc/internal/platform"
)

// cmdSwapUsage reads vm.swapusage. Fixed argv, no shell.
var cmdSwapUsage = platform.Command{Name: "sysctl", Args: []string{"-n", "vm.swapusage"}}

// ParseSwapUsage parses `sysctl -n vm.swapusage`, e.g.
// "total = 5120.00M  used = 3838.06M  free = 1281.94M  (encrypted)".
// Units K, M and G are binary multipliers.
func ParseSwapUsage(out []byte) (Swap, error) {
	var s Swap
	fields := strings.Fields(string(out))
	seen := map[string]bool{}
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "(encrypted)" {
			s.Encrypted = true
			continue
		}
		var dst *int64
		switch f {
		case "total":
			dst = &s.TotalBytes
		case "used":
			dst = &s.UsedBytes
		case "free":
			dst = &s.FreeBytes
		default:
			continue
		}
		if i+2 >= len(fields) || fields[i+1] != "=" {
			return Swap{}, fmt.Errorf("probe: vm.swapusage: malformed %q field", f)
		}
		n, err := parseSwapSize(fields[i+2])
		if err != nil {
			return Swap{}, fmt.Errorf("probe: vm.swapusage: %s: %w", f, err)
		}
		*dst = n
		seen[f] = true
		i += 2
	}
	for _, k := range []string{"total", "used", "free"} {
		if !seen[k] {
			return Swap{}, fmt.Errorf("probe: vm.swapusage: missing %q", k)
		}
	}
	return s, nil
}

// parseSwapSize converts "5120.00M" to bytes (binary multipliers, rounded).
func parseSwapSize(v string) (int64, error) {
	if v == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := 1.0
	switch v[len(v)-1] {
	case 'K':
		mult = 1 << 10
	case 'M':
		mult = 1 << 20
	case 'G':
		mult = 1 << 30
	}
	num := v
	if mult != 1 {
		num = v[:len(v)-1]
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) || f*mult >= math.MaxInt64 {
		return 0, fmt.Errorf("bad size %q", v)
	}
	return int64(math.Round(f * mult)), nil
}

// CollectSwap runs sysctl -n vm.swapusage through r.
func CollectSwap(ctx context.Context, r platform.Runner) (Swap, error) {
	out, err := runReadOnly(ctx, r, cmdSwapUsage)
	if err != nil {
		return Swap{}, err
	}
	return ParseSwapUsage(out)
}
