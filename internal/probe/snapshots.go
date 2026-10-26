package probe

import (
	"bufio"
	"bytes"
	"context"
	"strings"

	"macdoc/internal/platform"
)

var cmdListSnapshots = platform.Command{Name: "tmutil", Args: []string{"listlocalsnapshots", "/"}}

// ParseLocalSnapshots parses `tmutil listlocalsnapshots /`. The
// "Snapshots for disk /:" header and blank lines are skipped; every other
// trimmed line is a snapshot name. Header-only output, or a line saying there
// are no snapshots, gives Count 0. It never fails today; the error is part of
// the contract for future formats.
func ParseLocalSnapshots(out []byte) (Snapshots, error) {
	s := Snapshots{Names: []string{}}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || isSnapshotsHeader(line) || isNoSnapshots(line) {
			continue
		}
		s.Names = append(s.Names, line)
	}
	if err := sc.Err(); err != nil {
		return Snapshots{}, err
	}
	s.Count = len(s.Names)
	return s, nil
}

func isSnapshotsHeader(line string) bool {
	return strings.HasPrefix(line, "Snapshots for ") && strings.HasSuffix(line, ":")
}

// isNoSnapshots matches messages such as "No local snapshots" or
// "There are no snapshots".
func isNoSnapshots(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "snapshots") && (strings.HasPrefix(l, "no ") || strings.Contains(l, " no "))
}

// CollectSnapshots runs tmutil listlocalsnapshots / through r.
func CollectSnapshots(ctx context.Context, r platform.Runner) (Snapshots, error) {
	out, err := runRead(ctx, r, cmdListSnapshots)
	if err != nil {
		return Snapshots{}, err
	}
	return ParseLocalSnapshots(out)
}
