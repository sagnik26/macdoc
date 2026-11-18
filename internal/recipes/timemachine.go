package recipes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagnik26/macdoc/internal/fsread"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

func init() {
	Register("incomplete_backups", detectIncompleteBackups)
}

// detectIncompleteBackups reports interrupted Time Machine backups: folders
// ending ".inProgress" under each destination's Backups.backupdb. It reads
// only folder names through `tmutil destinationinfo` and a directory listing,
// never a backup's contents, and never changes anything: removing one needs
// administrator rights, so this is advice only. Time Machine doesn't report
// the size of an interrupted backup, so Bytes is unknown.
func detectIncompleteBackups(ctx context.Context, env Env, _ Recipe) ([]Finding, error) {
	if env.Runner == nil || !ToolInstalled("tmutil") {
		return nil, nil
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: "tmutil", Args: []string{"destinationinfo"}})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(string(res.Stderr), "No destinations") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmutil destinationinfo: exit %d: %s", res.ExitCode, FirstLine(res.Stderr))
	}
	n := 0
	for _, mount := range destinationMounts(string(res.Stdout)) {
		n += countInProgress(filepath.Join(mount, "Backups.backupdb"))
	}
	if n == 0 {
		return nil, nil
	}
	return []Finding{{
		Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "Time Machine doesn't report the size of an interrupted backup"},
		Note:  Plural(n, "incomplete backup", "incomplete backups"),
	}}, nil
}

// destinationMounts reads the "Mount Point" lines of `tmutil destinationinfo`:
// absolute paths only.
func destinationMounts(out string) []string {
	var mounts []string
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, ":")
		val = strings.TrimSpace(val)
		if ok && strings.TrimSpace(key) == "Mount Point" && filepath.IsAbs(val) && !strings.Contains(val, "..") {
			mounts = append(mounts, val)
		}
	}
	return mounts
}

// countInProgress counts ".inProgress" folders one level below each machine
// folder of a Backups.backupdb. An unreadable folder counts as none.
func countInProgress(backupdb string) int {
	if !fsread.Dir(backupdb) {
		return 0
	}
	machines, err := os.ReadDir(backupdb)
	if err != nil {
		return 0
	}
	n := 0
	for _, m := range machines {
		if !m.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(backupdb, m.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".inProgress") {
				n++
			}
		}
	}
	return n
}
