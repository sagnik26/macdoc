package recipes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sagnik26/macdoc/internal/model"
)

func init() {
	Register("venv_stale", detectVenvStale)
	Register("conda_pkgs", DetectCondaPkgs)
}

// pythonProjectFiles mark a directory as a Python project (besides
// requirements-*.txt, see IsPythonProject).
var pythonProjectFiles = map[string]bool{
	"pyproject.toml": true, "requirements.txt": true, "setup.py": true, "setup.cfg": true,
	"Pipfile": true, "poetry.lock": true, "uv.lock": true,
}

// IsPythonProject reports whether dir directly holds a regular project file.
// It reads names only (os.ReadDir); a symlinked marker doesn't count.
func IsPythonProject(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && (pythonProjectFiles[n] ||
			strings.HasPrefix(n, "requirements-") && strings.HasSuffix(n, ".txt")) {
			return true
		}
	}
	return false
}

// detectVenvStale emits project .venv directories that contain pyvenv.cfg
// (checked with os.Lstat, so a symlinked marker doesn't count), whose parent
// is a Python project (IsPythonProject) and whose newest mtime is older than
// stale_days. Hidden and Library trees are skipped, and the generic name
// "venv" is not matched.
func detectVenvStale(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Nodes == nil {
		return nil, nil
	}
	nodes, err := ProjectDirs(env, ".venv")
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range nodes {
		if n.Bytes <= 0 || !Stale(env, n.MtimeMax, r.StaleDays) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(n.Path, "pyvenv.cfg"))
		if err != nil || !fi.Mode().IsRegular() || !IsPythonProject(filepath.Dir(n.Path)) {
			continue
		}
		f := nodeFinding(n)
		f.Title = ".venv in " + filepath.Base(filepath.Dir(n.Path))
		if !WithProject(env, &f, filepath.Dir(n.Path), r.StaleDays) {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// condaRemoveRe matches conda clean's dry-run lines that carry a size, e.g.
// "Will remove 12 (345.6 MB) tarball(s)." (testdata/w2_conda_clean_dry_run.txt).
var condaRemoveRe = regexp.MustCompile(`Will remove (\d+) \(([0-9.,]+\s*[KMGT]?B)\) (.+?)\.?$`)

// DetectCondaPkgs runs `conda clean --all --dry-run` and sums the sizes it
// reports. conda's human_bytes uses binary units.
func DetectCondaPkgs(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Runner == nil || !ToolInstalled("conda") {
		return nil, nil
	}
	res, err := RunDryRun(ctx, env, r, BrewTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("conda clean --dry-run: exit %d: %s", res.ExitCode, FirstLine(res.Stderr))
	}
	var total int64
	var parts []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		m := condaRemoveRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		b, ok := ParseSize(m[2], 1024)
		if !ok {
			return nil, fmt.Errorf("conda clean --dry-run: bad size %q", m[2])
		}
		if n, _ := strconv.Atoi(m[1]); n > 0 && b > 0 {
			if total, ok = AddSize(total, b); !ok {
				return []Finding{{Bytes: implausibleSize("conda clean --dry-run"), Note: "Would remove " + strings.Join(append(parts, m[1]+" "+m[3]), ", ")}}, nil
			}
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
