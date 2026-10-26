package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"macdoc/internal/safety"
)

// realDir returns a canonical, symlink-free temp directory (t.TempDir lives
// under /var/folders, and /var is a symlink on macOS).
func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// setup points HOME at a fake home and returns (base, home).
func setup(t *testing.T) (string, string) {
	t.Helper()
	base := realDir(t)
	home := mkdir(t, filepath.Join(base, "Users", "dev"))
	t.Setenv("HOME", home)
	return base, home
}

func TestRootInactive(t *testing.T) {
	// D10: only an unset variable means inactive; set-but-empty fails closed
	// (TestRootSetButEmpty).
	setup(t)
	t.Setenv(EnvVar, "")
	if err := os.Unsetenv(EnvVar); err != nil {
		t.Fatal(err)
	}
	root, active, err := Root()
	if root != "" || active || err != nil {
		t.Errorf("Root() = %q, %v, %v; want inactive", root, active, err)
	}
	if err := Check("/anything/at/all"); err != nil {
		t.Errorf("Check with inactive sandbox = %v", err)
	}
}

func TestRootSetButEmpty(t *testing.T) {
	base, _ := setup(t)
	for _, v := range []string{"", " ", "\t\n"} {
		t.Setenv(EnvVar, v)
		root, active, err := Root()
		if root != "" || !active || err == nil || !strings.Contains(err.Error(), "set but empty") {
			t.Errorf("Root() with %q = %q, %v, %v; want \"\", true, set-but-empty error", v, root, active, err)
		}
		if err := Check(filepath.Join(base, "x")); err == nil {
			t.Errorf("Check with %s=%q = nil; want fail closed", EnvVar, v)
		}
	}
}

func TestRootValid(t *testing.T) {
	base, _ := setup(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	for _, v := range []string{sb, sb + "/", sb + "/./"} {
		t.Setenv(EnvVar, v)
		root, active, err := Root()
		if err != nil || !active || root != sb {
			t.Errorf("Root(%q) = %q, %v, %v; want %q", v, root, active, err, sb)
		}
	}
}

func TestRootSymlinked(t *testing.T) {
	base, _ := setup(t)
	real := mkdir(t, filepath.Join(base, "real"))
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, link)
	root, active, err := Root()
	if err != nil || !active || root != real {
		t.Fatalf("Root() = %q, %v, %v; want resolved %q", root, active, err, real)
	}
	// A canonical path under the real dir is inside.
	if err := Check(filepath.Join(real, "x")); err != nil {
		t.Errorf("Check(real/x) = %v", err)
	}
}

func TestRootRejects(t *testing.T) {
	base, home := setup(t)
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	homeLink := filepath.Join(base, "homelink")
	if err := os.Symlink(home, homeLink); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(base, "dangling")
	if err := os.Symlink(filepath.Join(base, "nope"), dangling); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, val, want string
	}{
		{"relative", "tmp/sb", "absolute"},
		{"dot relative", "./sb", "absolute"},
		{"missing", filepath.Join(base, "missing"), "no such file"},
		{"dangling symlink", dangling, "no such file"},
		{"file", file, "not a directory"},
		{"dotdot", base + "/sb/../sb", ".."},
		{"root", "/", "filesystem root"},
		{"root double slash", "//", "filesystem root"},
		{"home", home, "home directory"},
		{"home trailing slash", home + "/", "home directory"},
		{"home via symlink", homeLink, "home directory"},
		{"home different case", strings.ToUpper(home[:len(home)-3]) + "dev", "home directory"},
		{"parent of home", filepath.Dir(home), "contains the home"},
		{"grandparent of home", base, "contains the home"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(EnvVar, c.val)
			root, _, err := Root()
			if err == nil {
				t.Fatalf("Root(%q) = %q, nil; want error", c.val, root)
			}
			if root != "" {
				t.Errorf("Root(%q) returned root %q with error", c.val, root)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("Root(%q) error %q does not mention %q", c.val, err, c.want)
			}
			// Fail closed: every path is refused, even ones that would be
			// inside the attempted root.
			for _, p := range []string{filepath.Join(home, "x", "y"), filepath.Join(base, "sb", "x"), "/tmp/x"} {
				if err := Check(p); err == nil {
					t.Errorf("Check(%q) with invalid sandbox = nil; want error", p)
				}
			}
		})
	}
}

func TestRootRejectsAncestorOfRealHome(t *testing.T) {
	// Without overriding HOME: "/Users" (or whatever holds the real home)
	// must be rejected.
	h, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	h, err = filepath.EvalSymlinks(h)
	if err != nil {
		t.Skip("home not resolvable")
	}
	t.Setenv(EnvVar, filepath.Dir(h))
	if _, _, err := Root(); err == nil {
		t.Errorf("Root(%q) = nil error; want rejection of home ancestor", filepath.Dir(h))
	}
}

func TestRootHomeErrors(t *testing.T) {
	base := realDir(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	t.Setenv(EnvVar, sb)

	t.Setenv("HOME", "")
	if _, _, err := Root(); err == nil {
		t.Error("Root() with no HOME = nil error; want fail closed")
	}
	t.Setenv("HOME", "relative/home")
	if _, _, err := Root(); err == nil {
		t.Error("Root() with relative HOME = nil error; want fail closed")
	}
	if err := Check(filepath.Join(sb, "x")); err == nil {
		t.Error("Check() with relative HOME = nil; want fail closed")
	}
	// A home that does not exist still compares lexically.
	t.Setenv("HOME", filepath.Join(sb, "missing-home"))
	if _, _, err := Root(); err == nil {
		t.Error("Root() containing a missing HOME = nil error; want rejection")
	}
	t.Setenv("HOME", filepath.Join(base, "elsewhere"))
	if root, _, err := Root(); err != nil || root != sb {
		t.Errorf("Root() with missing unrelated HOME = %q, %v", root, err)
	}
}

func TestCheck(t *testing.T) {
	base, _ := setup(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	other := mkdir(t, filepath.Join(base, "sbother"))
	mkdir(t, filepath.Join(sb, "d"))
	t.Setenv(EnvVar, sb)

	cases := []struct {
		path string
		ok   bool
	}{
		{filepath.Join(sb, "d"), true},
		{filepath.Join(sb, "d", "e", "f"), true},
		{filepath.Join(sb, "missing"), true},
		{strings.ToUpper(sb) + "/d", true}, // APFS is case-insensitive
		{sb, false},                        // equal to root
		{sb + "/", false},
		{filepath.Dir(sb), false},
		{base, false},
		{other, false}, // sibling sharing a name prefix
		{filepath.Join(other, "x"), false},
		{"/", false},
		{"/tmp/x", false},
		{"relative/x", false},
		{"", false},
	}
	for _, c := range cases {
		err := Check(c.path)
		if (err == nil) != c.ok {
			t.Errorf("Check(%q) = %v; want ok=%v", c.path, err, c.ok)
		}
	}
}

func TestCheckSymlinkOut(t *testing.T) {
	base, _ := setup(t)
	sb := mkdir(t, filepath.Join(base, "sb"))
	outside := mkdir(t, filepath.Join(base, "outside"))
	link := filepath.Join(sb, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvVar, sb)

	// The link itself lives inside the sandbox; removing it is fine.
	c, err := safety.Canonicalize(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(c); err != nil {
		t.Errorf("Check(link itself %q) = %v; want nil", c, err)
	}
	// Anything reached through the link resolves outside and is refused.
	c, err = safety.Canonicalize(filepath.Join(link, "victim"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c, outside) {
		t.Fatalf("canonical form %q does not resolve through the link", c)
	}
	if err := Check(c); err == nil {
		t.Errorf("Check(%q) through escaping symlink = nil; want error", c)
	}
}
