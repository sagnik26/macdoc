package check

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// executorDir is the only package allowed to mutate the filesystem.
const executorDir = "internal/executor"

// errUsage means the flag package already printed the problem.
var errUsage = errors.New("usage")

// Run executes the check from dir and returns the process exit code:
// 0 clean, 1 violations, 2 usage or I/O error.
func Run(args []string, dir string, stdout, stderr io.Writer) int {
	lines, err := check(args, dir, stderr)
	if err != nil {
		if !errors.Is(err, errUsage) {
			_, _ = fmt.Fprintln(stderr, "forbidcheck:", err)
		}
		return 2
	}
	if len(lines) == 0 {
		return 0
	}
	if _, err := io.WriteString(stdout, strings.Join(lines, "\n")+"\n"); err != nil {
		return 2
	}
	return 1
}

// check returns one line per violation (findings, then stale allowlist
// entries).
func check(args []string, dir string, stderr io.Writer) ([]string, error) {
	fl := flag.NewFlagSet("forbidcheck", flag.ContinueOnError)
	fl.SetOutput(stderr)
	allowPath := fl.String("allow", "", "allowlist file (default <module>/tools/forbidcheck/allow.txt)")
	if err := fl.Parse(args); err != nil {
		return nil, errUsage
	}
	root, err := moduleRoot(dir)
	if err != nil {
		return nil, err
	}
	if *allowPath == "" {
		*allowPath = filepath.Join(root, "tools", "forbidcheck", "allow.txt")
	}
	allow, err := loadAllowlist(*allowPath)
	if err != nil {
		return nil, err
	}
	patterns := fl.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	files := map[string]bool{}
	for _, p := range patterns {
		if err := collect(root, dir, p, files); err != nil {
			return nil, err
		}
	}
	findings, used, err := checkFiles(root, sortedKeys(files), allow)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, f := range findings {
		lines = append(lines, f.String())
	}
	// Stale entries are only meaningful for a whole-module run.
	if wholeModule(root, dir, patterns) {
		var stale []string
		for k := range allow {
			if !used[k] {
				stale = append(stale, k)
			}
		}
		sort.Strings(stale)
		for _, k := range stale {
			lines = append(lines, fmt.Sprintf("%s: stale allowlist entry %s (remove it)", filepath.Base(*allowPath), k))
		}
	}
	return lines, nil
}

func loadAllowlist(p string) (Allowlist, error) {
	f, err := os.Open(p) //nolint:gosec // G304: reading the allowlist named on the command line is the point.
	if errors.Is(err, fs.ErrNotExist) {
		return Allowlist{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only; Close cannot lose data
	return ParseAllowlist(f)
}

// checkFiles checks each module-relative file and drops allowlisted
// findings. used records which allowlist entries matched.
func checkFiles(root string, files []string, allow Allowlist) ([]Finding, map[string]bool, error) {
	var out []Finding
	used := map[string]bool{}
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) //nolint:gosec // G304: reads module sources found by collect.
		if err != nil {
			return nil, nil, err
		}
		found, err := Source(rel, src)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range found {
			if f.Func != "" && allow[f.key()] {
				used[f.key()] = true
				continue
			}
			out = append(out, f)
		}
	}
	return out, used, nil
}

// moduleRoot finds the directory containing go.mod at or above dir.
func moduleRoot(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod found at or above %s", dir)
		}
		d = parent
	}
}

// collect adds the in-scope files matched by pattern to files, as
// module-relative slash paths.
func collect(root, dir, pattern string, files map[string]bool) error {
	recursive := false
	if p, ok := strings.CutSuffix(pattern, "/..."); ok {
		pattern, recursive = p, true
	} else if pattern == "..." {
		pattern, recursive = ".", true
	}
	start, err := filepath.Abs(filepath.Join(dir, pattern))
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(root, start); err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("pattern %q is outside the module", pattern)
	}
	fi, err := os.Stat(start)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("pattern %q is not a directory", pattern)
	}
	return filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p == start {
				return nil
			}
			if !recursive || skipDir(p, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if inScope(rel) {
			files[rel] = true
		}
		return nil
	})
}

// skipDir mirrors the go tool: testdata, vendor, and "." or "_" prefixed
// directories are not packages, and nested modules are not ours.
func skipDir(p, name string) bool {
	if name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	_, err := os.Stat(filepath.Join(p, "go.mod"))
	return err == nil
}

func inScope(rel string) bool {
	if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return false
	}
	return rel != executorDir && !strings.HasPrefix(rel, executorDir+"/")
}

func wholeModule(root, dir string, patterns []string) bool {
	for _, p := range patterns {
		p, ok := strings.CutSuffix(p, "...")
		if !ok {
			continue
		}
		if a, err := filepath.Abs(filepath.Join(dir, p)); err == nil && a == root {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
