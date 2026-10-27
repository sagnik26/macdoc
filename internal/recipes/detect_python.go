package recipes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"macdoc/internal/model"
)

func init() {
	Register("venv_stale", detectVenvStale)
	Register("conda_pkgs", detectCondaPkgs)
}

// detectVenvStale emits top-level .venv directories that contain pyvenv.cfg
// (checked with os.Lstat, so a symlinked marker doesn't count) and whose
// newest mtime is older than stale_days. The generic name "venv" is not
// matched.
func detectVenvStale(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Nodes == nil {
		return nil, nil
	}
	nodes, err := projectDirs(env, ".venv")
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range nodes {
		if n.Bytes <= 0 || !stale(env, n.MtimeMax, r.StaleDays) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(n.Path, "pyvenv.cfg"))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		f := nodeFinding(n)
		f.Title = ".venv in " + filepath.Base(filepath.Dir(n.Path))
		out = append(out, f)
	}
	return out, nil
}

// condaRemoveRe matches conda clean's dry-run lines that carry a size, e.g.
// "Will remove 12 (345.6 MB) tarball(s)." (testdata/w2_conda_clean_dry_run.txt).
var condaRemoveRe = regexp.MustCompile(`Will remove (\d+) \(([0-9.,]+\s*[KMGT]?B)\) (.+?)\.?$`)

// detectCondaPkgs runs `conda clean --all --dry-run` and sums the sizes it
// reports. conda's human_bytes uses binary units.
func detectCondaPkgs(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Runner == nil || !ToolInstalled("conda") {
		return nil, nil
	}
	res, err := runDryRun(ctx, env, r, brewTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("conda clean --dry-run: exit %d: %s", res.ExitCode, firstLine(res.Stderr))
	}
	var total int64
	var parts []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		m := condaRemoveRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		b, ok := parseSize(m[2], 1024)
		if !ok {
			return nil, fmt.Errorf("conda clean --dry-run: bad size %q", m[2])
		}
		if n, _ := strconv.Atoi(m[1]); n > 0 && b > 0 {
			total += b
			parts = append(parts, m[1]+" "+m[3])
		}
	}
	if total == 0 {
		return nil, nil
	}
	return []Finding{{
		Bytes: model.Bytes{Value: total, Source: model.SourceTool},
		Note:  "Would remove " + strings.Join(parts, ", "),
	}}, nil
}
