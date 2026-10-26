package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findings(t *testing.T, src string) []string {
	t.Helper()
	fs, err := checkSource("p/f.go", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	for _, f := range fs {
		out = append(out, f.String())
	}
	return out
}

func TestCheckSource(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"direct", `package p
import "os"
func f() { os.RemoveAll("/x") }`,
			[]string{"p/f.go:3: forbidden call os.RemoveAll (only internal/executor may delete)"}},
		{"all os", `package p
import "os"
func f() {
	os.Remove("a")
	os.Rename("a", "b")
	os.Truncate("a", 0)
}`,
			[]string{
				"p/f.go:4: forbidden call os.Remove (only internal/executor may delete)",
				"p/f.go:5: forbidden call os.Rename (only internal/executor may delete or move)",
				"p/f.go:6: forbidden call os.Truncate (only internal/executor may delete)",
			}},
		{"aliased", `package p
import x "os"
func f() { x.Remove("a") }`,
			[]string{"p/f.go:3: forbidden call os.Remove (only internal/executor may delete)"}},
		{"dot import", `package p
import . "os"
func f() { RemoveAll("a"); Getenv("HOME") }`,
			[]string{"p/f.go:3: forbidden call os.RemoveAll (only internal/executor may delete)"}},
		{"function value", `package p
import "os"
var rm = os.Remove`,
			[]string{"p/f.go:3: forbidden call os.Remove (only internal/executor may delete)"}},
		{"syscall", `package p
import "syscall"
func f() { syscall.Unlink("a"); syscall.Rmdir("a"); syscall.Rename("a", "b"); syscall.Getpid() }`,
			[]string{
				"p/f.go:3: forbidden call syscall.Unlink (only internal/executor may delete)",
				"p/f.go:3: forbidden call syscall.Rmdir (only internal/executor may delete)",
				"p/f.go:3: forbidden call syscall.Rename (only internal/executor may delete or move)",
			}},
		{"unix", `package p
import u "golang.org/x/sys/unix"
func f() { u.Unlinkat(0, "a", 0); u.Renameat(0, "a", 0, "b"); u.RenamexNp("a", "b", 0); u.Rmdir("a"); u.Getpid() }`,
			[]string{
				"p/f.go:3: forbidden call unix.Unlinkat (only internal/executor may delete)",
				"p/f.go:3: forbidden call unix.Renameat (only internal/executor may delete or move)",
				"p/f.go:3: forbidden call unix.RenamexNp (only internal/executor may delete or move)",
				"p/f.go:3: forbidden call unix.Rmdir (only internal/executor may delete)",
			}},
		{"exec", `package p
import "os/exec"
func f() { exec.Command("ls") }`,
			[]string{"p/f.go:3: forbidden call exec.Command (only internal/executor may run mutating commands)"}},
		{"exec shell", `package p
import "os/exec"
func f() { exec.Command("sh", "-c", "rm -rf /") }`,
			[]string{
				"p/f.go:3: forbidden call exec.Command (only internal/executor may run mutating commands)",
				`p/f.go:3: forbidden shell "sh" as command name (no shell, no sudo)`,
			}},
		{"exec context shell path", `package p
import (
	"context"
	e "os/exec"
)
func f(ctx context.Context) {
	e.CommandContext(ctx,
		"/usr/bin/sudo", "rm")
}`,
			[]string{
				"p/f.go:7: forbidden call exec.CommandContext (only internal/executor may run mutating commands)",
				`p/f.go:8: forbidden shell "/usr/bin/sudo" as command name (no shell, no sudo)`,
			}},
		{"exec non-literal name", `package p
import "os/exec"
func f(n string) { exec.Command(n, "sh") }`,
			[]string{"p/f.go:3: forbidden call exec.Command (only internal/executor may run mutating commands)"}},
		{"start process", `package p
import "os"
func f() { os.StartProcess("bash", nil, nil) }`,
			[]string{
				"p/f.go:3: forbidden call os.StartProcess (only internal/executor may run mutating commands)",
				`p/f.go:3: forbidden shell "bash" as command name (no shell, no sudo)`,
			}},
		{"unix exec", `package p
import "golang.org/x/sys/unix"
func f() { unix.Exec("/bin/zsh", nil, nil) }`,
			[]string{
				"p/f.go:3: forbidden call unix.Exec (only internal/executor may run mutating commands)",
				`p/f.go:3: forbidden shell "/bin/zsh" as command name (no shell, no sudo)`,
			}},
		{"harmless os", `package p
import "os"
func f() { os.Getenv("x"); os.MkdirAll("a", 0o755); os.Symlink("a", "b") }`, nil},
		{"method named Remove", `package p
import "os"
type T struct{}
func (T) Remove(string) {}
func f(t T, m map[string]int) { t.Remove("a"); delete(m, "a"); _ = os.Args }`, nil},
		{"shadowed package name", `package p
type fake struct{}
func (fake) RemoveAll(string) {}
func f() { var os fake; os.RemoveAll("a") }`, nil},
		{"blank import", `package p
import _ "os"`, nil},
		{"no imports", `package p
func f() {}`, nil},
		{"raw string shell", "package p\nimport \"os/exec\"\nfunc f() { exec.Command(`zsh`) }",
			[]string{
				"p/f.go:3: forbidden call exec.Command (only internal/executor may run mutating commands)",
				`p/f.go:3: forbidden shell "zsh" as command name (no shell, no sudo)`,
			}},
		{"number arg", `package p
import "os/exec"
func f() { exec.Command(1) }`,
			[]string{"p/f.go:3: forbidden call exec.Command (only internal/executor may run mutating commands)"}},
		{"selector on call result", `package p
import "os"
func g() os.File { return os.File{} }
func f() { g().Close() }`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findings(t, c.src)
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}

func TestCheckSourceParseError(t *testing.T) {
	if _, err := checkSource("bad.go", []byte("package")); err == nil {
		t.Error("want parse error")
	}
}

func TestParseAllowlist(t *testing.T) {
	a, err := parseAllowlist(strings.NewReader("# comment\n\ninternal/platform/runner.go:exec.CommandContext  # why\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || !a["internal/platform/runner.go:exec.CommandContext"] {
		t.Errorf("allowlist = %v", a)
	}
	for _, bad := range []string{"nocolon\n", ":os.Remove\n", "a.go:Remove\n"} {
		if _, err := parseAllowlist(strings.NewReader(bad)); err == nil {
			t.Errorf("parseAllowlist(%q) = nil error", bad)
		}
	}
}

// module writes a throwaway module and returns its root.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module m\n"
	for p, src := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const removeSrc = "package p\nimport \"os\"\nfunc f() { os.RemoveAll(\"x\") }\n"

func runIn(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, dir, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunScope(t *testing.T) {
	root := module(t, map[string]string{
		"a/a.go":                       removeSrc,
		"a/a_test.go":                  removeSrc,         // tests ignored
		"internal/executor/x.go":       removeSrc,         // executor allowed
		"internal/executor/sub/y.go":   removeSrc,         // executor subpackages too
		"internal/executorx/z.go":      removeSrc,         // look-alike is not
		"a/testdata/t.go":              removeSrc,         // not a package
		".hidden/h.go":                 removeSrc,         // not a package
		"_skip/s.go":                   removeSrc,         // not a package
		"vendor/v/v.go":                removeSrc,         // not ours
		"nested/go.mod":                "module nested\n", // nested module
		"nested/n.go":                  removeSrc,
		"internal/platform/runner.go":  "package platform\nimport \"os/exec\"\nvar _ = exec.CommandContext\n",
		"tools/forbidcheck/allow.txt":  "internal/platform/runner.go:exec.CommandContext\n",
		"notes.txt":                    "os.RemoveAll",
		"internal/platform/fake.go":    "package platform\n",
		"internal/platform/README.txt": "",
	})
	code, out, errOut := runIn(t, root, "./...")
	want := "a/a.go:3: forbidden call os.RemoveAll (only internal/executor may delete)\n" +
		"internal/executorx/z.go:3: forbidden call os.RemoveAll (only internal/executor may delete)\n"
	if code != 1 || out != want {
		t.Errorf("code %d, out:\n%s\nstderr: %s\nwant:\n%s", code, out, errOut, want)
	}

	// Default pattern is ./..., and running from a subdirectory finds the module.
	if code, out, _ := runIn(t, filepath.Join(root, "a")); code != 1 || !strings.Contains(out, "a/a.go:3") {
		t.Errorf("subdir run: code %d out %q", code, out)
	}
	// Single-directory pattern does not recurse.
	if code, out, _ := runIn(t, root, "./internal"); code != 0 || out != "" {
		t.Errorf("./internal: code %d out %q", code, out)
	}
	if code, out, _ := runIn(t, root, "./internal/..."); code != 1 || strings.Contains(out, "a/a.go") {
		t.Errorf("./internal/...: code %d out %q", code, out)
	}
	if code, _, _ := runIn(t, filepath.Join(root, "internal", "platform"), "..."); code != 0 {
		t.Errorf("...: code %d", code)
	}
}

func TestRunClean(t *testing.T) {
	root := module(t, map[string]string{
		"internal/platform/runner.go": "package platform\nimport \"os/exec\"\nvar _ = exec.CommandContext\n",
		"tools/forbidcheck/allow.txt": "# ok\ninternal/platform/runner.go:exec.CommandContext\n",
	})
	if code, out, errOut := runIn(t, root, "./..."); code != 0 || out != "" || errOut != "" {
		t.Errorf("code %d out %q err %q", code, out, errOut)
	}
}

func TestRunAllowlistIsPerFileAndFunc(t *testing.T) {
	root := module(t, map[string]string{
		// Same func, other file: not allowed.
		"internal/platform/other.go": "package platform\nimport \"os/exec\"\nvar _ = exec.CommandContext\n",
		// Allowed file, other func: not allowed.
		"internal/platform/runner.go": "package platform\nimport (\"os\"; \"os/exec\")\nvar _ = exec.CommandContext\nvar _ = os.Remove\n",
		"tools/forbidcheck/allow.txt": "internal/platform/runner.go:exec.CommandContext\n",
	})
	code, out, _ := runIn(t, root)
	if code != 1 || !strings.Contains(out, "other.go:3: forbidden call exec.CommandContext") ||
		!strings.Contains(out, "runner.go:4: forbidden call os.Remove") || strings.Contains(out, "runner.go:3") {
		t.Errorf("code %d out:\n%s", code, out)
	}
}

func TestRunShellNotAllowlistable(t *testing.T) {
	root := module(t, map[string]string{
		"internal/platform/runner.go": "package platform\nimport \"os/exec\"\nvar _ = exec.Command(\"sh\")\n",
		"tools/forbidcheck/allow.txt": "internal/platform/runner.go:exec.Command\n",
	})
	code, out, _ := runIn(t, root)
	if code != 1 || !strings.Contains(out, `forbidden shell "sh"`) || strings.Contains(out, "forbidden call") {
		t.Errorf("code %d out:\n%s", code, out)
	}
}

func TestRunStaleAllowlist(t *testing.T) {
	root := module(t, map[string]string{
		"a/a.go":                      "package a\n",
		"tools/forbidcheck/allow.txt": "internal/platform/runner.go:exec.CommandContext\n",
	})
	code, out, _ := runIn(t, root, "./...")
	if code != 1 || !strings.Contains(out, "stale allowlist entry internal/platform/runner.go:exec.CommandContext") {
		t.Errorf("code %d out %q", code, out)
	}
	// Partial runs do not report stale entries.
	if code, out, _ := runIn(t, root, "./a/..."); code != 0 || out != "" {
		t.Errorf("partial: code %d out %q", code, out)
	}
}

func TestRunMissingAllowlistMeansEmpty(t *testing.T) {
	root := module(t, map[string]string{
		"internal/platform/runner.go": "package platform\nimport \"os/exec\"\nvar _ = exec.CommandContext\n",
	})
	if code, out, _ := runIn(t, root); code != 1 || !strings.Contains(out, "exec.CommandContext") {
		t.Errorf("code %d out %q", code, out)
	}
}

func TestRunErrors(t *testing.T) {
	root := module(t, map[string]string{
		"a/a.go":   "package a\n",
		"bad/b.go": "package\n",
		"f.txt":    "",
	})
	badAllow := filepath.Join(root, "bad.txt")
	if err := os.WriteFile(badAllow, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		dir  string
		args []string
	}{
		{"bad flag", root, []string{"-nope"}},
		{"no module", t.TempDir(), nil},
		{"bad allowlist", root, []string{"-allow", badAllow, "./a"}},
		{"allowlist is dir", root, []string{"-allow", root, "./a"}},
		{"outside module", root, []string{"../..."}},
		{"missing dir", root, []string{"./missing/..."}},
		{"pattern is file", root, []string{"./f.txt"}},
		{"parse error", root, []string{"./bad"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, out, errOut := runIn(t, c.dir, c.args...); code != 2 {
				t.Errorf("code %d out %q err %q; want 2", code, out, errOut)
			}
		})
	}
}

// TestRepoIsClean runs the checker on this repository, as CI does.
func TestRepoIsClean(t *testing.T) {
	if code, out, errOut := runIn(t, ".", "../../..."); code != 0 {
		t.Errorf("forbidcheck on repo: code %d\n%s%s", code, out, errOut)
	}
}
