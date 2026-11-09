package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/safety"
)

// The tests in this file pin which rule fires, not just that something
// protects a path. Mutation testing (plan.md claim 2) showed that tests
// asserting only "protected: yes" let boundary and error-path changes in
// internal/safety survive, because another rule still protects the path.

const reasonsHome = "/Users/macdoc-test-nobody"

func reasonsPolicy(t *testing.T) *safety.Policy {
	t.Helper()
	p, err := safety.NewPolicy(reasonsHome, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIsProtectedReasons(t *testing.T) {
	p := reasonsPolicy(t)
	tests := []struct {
		name, path, root string
	}{
		{"filesystem root", "/", "/"},
		{"relative path fails closed", "relative/x", "not an absolute path"},
		// The parent of home is an ancestor of home, not a sibling of it. A
		// boundary slip here changes the reported rule to "outside home".
		{"parent of home", "/Users", "ancestor of " + reasonsHome},
		{"parent of home, other case", "/USERS", "ancestor of " + reasonsHome},
		{"sibling of home", "/Users/other", "outside home: /Users"},
		{"below a sibling of home", "/Users/other/x/y", "outside home: /Users"},
		{"system root", "/System", "/System"},
		{"below a system root", "/usr/local/bin", "/usr"},
		{"home-relative root", reasonsHome + "/Documents", reasonsHome + "/Documents"},
		{"below a home-relative root", reasonsHome + "/.ssh/id_ed25519", reasonsHome + "/.ssh"},
		{"ancestor of a home-relative root", reasonsHome + "/Library/Application Support/MobileSync", safety.ContainsProtected + reasonsHome + "/Library/Application Support/MobileSync/Backup"},
		{"Containers itself", reasonsHome + "/Library/Containers", safety.ContainsProtected + reasonsHome + "/Library/Containers/com.apple.*"},
		{"Apple container", reasonsHome + "/Library/Containers/com.apple.mail/Data", reasonsHome + "/Library/Containers/com.apple.*"},
		{"git directory", reasonsHome + "/code/app/.git/objects", "any .git directory"},
		{"env file", reasonsHome + "/code/app/.env.local", ".env* files"},
	}
	for _, tt := range tests {
		got, root := p.IsProtected(tt.path)
		if !got || root != tt.root {
			t.Errorf("%s: IsProtected(%q) = %v, %q; want true, %q", tt.name, tt.path, got, root, tt.root)
		}
	}
}

func TestIsProtectedNotProtectedBoundaries(t *testing.T) {
	p := reasonsPolicy(t)
	for _, path := range []string{
		reasonsHome,                       // home itself is left to the depth rule
		reasonsHome + "/.gradle",          // below home, no rule
		reasonsHome + "/.gradle/caches",   // below home, no rule
		reasonsHome + "/Library/Caches/x", // Library is not protected as a whole
		reasonsHome + "/Library/Containers/org.example.app/x",
	} {
		if got, root := p.IsProtected(path); got {
			t.Errorf("IsProtected(%q) = true (%s), want false", path, root)
		}
	}
}

func TestNewPolicyErrorsNameTheCause(t *testing.T) {
	good := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(good, 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		home  string
		extra []string
		want  string
	}{
		{"relative home", "relative/home", nil, "is not absolute"},
		{"home with NUL", "/Users/a\x00b", nil, "NUL"},
		{"home with ..", "/Users/a/../b", nil, ".."},
		{"relative extra", good, []string{"relative/x"}, `protected path "relative/x" is not absolute`},
		{"extra with ..", good, []string{"/Users/a/../b"}, "safety: protected path:"},
		{"extra with NUL", good, []string{"/Users/a\x00b"}, "NUL"},
	}
	for _, tt := range tests {
		_, err := safety.NewPolicy(tt.home, tt.extra)
		if err == nil {
			t.Errorf("%s: NewPolicy succeeded", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error %q does not contain %q", tt.name, err, tt.want)
		}
	}
}

func TestNewPolicyExtraTildeIsHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := safety.NewPolicy(home, []string{"~"})
	if err != nil {
		t.Fatal(err)
	}
	got, root := p.IsProtected(filepath.Join(p.Home(), "x", "y"))
	if !got || root != p.Home() {
		t.Errorf("IsProtected below an extra %q = %v, %q; want true, %q", "~", got, root, p.Home())
	}
}

func TestCanonicalizeUnreadableParentIsRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode 000 directory")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: a test directory must be searchable again so t.TempDir can remove it.

	// Lstat of locked/child fails with EACCES, which is not "missing": the
	// path can't be resolved, so it must be refused, not guessed at.
	_, err := safety.Canonicalize(filepath.Join(dir, "child", "leaf"))
	if got := violationRule(t, err); got != safety.RuleInvalid {
		t.Fatalf("rule = %q (err %v), want %q", got, err, safety.RuleInvalid)
	}
	if !strings.Contains(err.Error(), "cannot resolve parent") {
		t.Errorf("error %q does not say why", err)
	}
}

func TestWithinRequiresAbsoluteRoot(t *testing.T) {
	// "a" and "/a" fold to the same Components, so only the absolute check
	// stops a relative root from matching.
	for _, tt := range []struct{ root, path string }{
		{"a", "/a"},
		{"a", "/a/b"},
		{"/a", "a"},
		{"a", "a"},
	} {
		if safety.Within(tt.root, tt.path) {
			t.Errorf("Within(%q, %q) = true, want false", tt.root, tt.path)
		}
	}
	if !safety.Within("/a", "/a/b") {
		t.Error("Within(/a, /a/b) = false, want true")
	}
}
