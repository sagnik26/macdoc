package tests

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/safety"
)

// newTestPolicy returns a policy whose home is a fresh directory under
// t.TempDir() (so under /private/var/folders on macOS).
func newTestPolicy(t *testing.T, extra ...string) *safety.Policy {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := safety.NewPolicy(home, extra)
	if err != nil {
		t.Fatalf("NewPolicy(%q): %v", home, err)
	}
	return p
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func violationRule(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var v *safety.Violation
	if !errors.As(err, &v) {
		t.Fatalf("error %v is %T, want *safety.Violation", err, err)
	}
	return v.Rule
}

func TestCanonicalizeRejects(t *testing.T) {
	tests := []struct {
		in   string
		rule string
	}{
		{"", safety.RuleInvalid},
		{"/a\x00b", safety.RuleInvalid},
		{"a/b", safety.RuleNotAbsolute},
		{".", safety.RuleNotAbsolute},
		{"~/x", safety.RuleNotAbsolute},
		{"/a/..", safety.RuleInvalid},
		{"/a/../b", safety.RuleInvalid},
		{"/..", safety.RuleInvalid},
		{"/a/b/../", safety.RuleInvalid},
		{"//a/../../b", safety.RuleInvalid},
	}
	for _, tt := range tests {
		_, err := safety.Canonicalize(tt.in)
		if got := violationRule(t, err); got != tt.rule {
			t.Errorf("Canonicalize(%q) rule = %q, want %q", tt.in, got, tt.rule)
		}
	}
}

func TestCanonicalizeMacOSFirmlinks(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/", "/"},
		{"/var/folders/zz/does-not-exist/x", "/private/var/folders/zz/does-not-exist/x"},
		{"/tmp/macdoc-nonexistent/a", "/private/tmp/macdoc-nonexistent/a"},
		{"/etc/hosts/x/y", "/private/etc/hosts/x/y"}, // ancestor is a file
		{"/tmp", "/tmp"},                             // final component not followed
		{"/a/./b//c", "/a/b/c"},                      // D2: a trailing "/" is now rejected; inner "." and "//" are cleaned
		{"/a/..foo/foo../...", "/a/..foo/foo../..."}, // not ".." safety.Components
		{"/TMP/x", "/private/tmp/x"},                 // case-insensitive lookup; link target used
	}
	for _, tt := range tests {
		got, err := safety.Canonicalize(tt.in)
		if err != nil {
			t.Errorf("Canonicalize(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Canonicalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	dir := t.TempDir()
	if !strings.HasPrefix(dir, "/var/folders/") {
		t.Skipf("TempDir %q is not under /var/folders", dir)
	}
	got, err := safety.Canonicalize(filepath.Join(dir, "x"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "/private" + dir + "/x"; got != want {
		t.Errorf("Canonicalize(tempdir/x) = %q, want %q", got, want)
	}
}

func TestCanonicalizeIdempotent(t *testing.T) {
	dir := t.TempDir()
	mustMkdirAll(t, filepath.Join(dir, "real", "sub"))
	mustSymlink(t, filepath.Join(dir, "real"), filepath.Join(dir, "link"))
	for _, in := range []string{
		filepath.Join(dir, "link", "sub", "x"),
		filepath.Join(dir, "link", "missing", "deeper", "x"),
		filepath.Join(dir, "link"),
		"/var/folders",
		"/Users/nobody/Library/Caches/x",
	} {
		c1, err := safety.Canonicalize(in)
		if err != nil {
			t.Fatalf("Canonicalize(%q): %v", in, err)
		}
		c2, err := safety.Canonicalize(c1)
		if err != nil {
			t.Fatalf("Canonicalize(%q): %v", c1, err)
		}
		if c1 != c2 {
			t.Errorf("not idempotent: %q -> %q -> %q", in, c1, c2)
		}
	}
}

func TestCanonicalizeNFC(t *testing.T) {
	nfd := "/nonexistent-macdoc/Café/x"
	got, err := safety.Canonicalize(nfd)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/nonexistent-macdoc/Café/x"; got != want {
		t.Errorf("Canonicalize(NFD) = %q, want %q", got, want)
	}
	// Case is preserved.
	got, err = safety.Canonicalize("/NonExistent-Macdoc/MiXeD")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/NonExistent-Macdoc/MiXeD" {
		t.Errorf("case not preserved: %q", got)
	}
}

func TestCanonicalizeSymlinks(t *testing.T) {
	dir, err := safety.Canonicalize(filepath.Join(t.TempDir(), "d"))
	if err != nil {
		t.Fatal(err)
	}
	mustMkdirAll(t, filepath.Join(dir, "real"))
	mustSymlink(t, filepath.Join(dir, "real"), filepath.Join(dir, "parent"))
	mustSymlink(t, "real", filepath.Join(dir, "relparent"))
	mustSymlink(t, filepath.Join(dir, "real"), filepath.Join(dir, "final"))
	mustSymlink(t, filepath.Join(dir, "loopb"), filepath.Join(dir, "loopa"))
	mustSymlink(t, filepath.Join(dir, "loopa"), filepath.Join(dir, "loopb"))
	mustSymlink(t, filepath.Join(dir, "nowhere"), filepath.Join(dir, "dangling"))

	ok := []struct{ in, want string }{
		{filepath.Join(dir, "parent", "x"), filepath.Join(dir, "real", "x")},
		{filepath.Join(dir, "relparent", "a", "b"), filepath.Join(dir, "real", "a", "b")},
		{filepath.Join(dir, "final"), filepath.Join(dir, "final")},
		{filepath.Join(dir, "loopa"), filepath.Join(dir, "loopa")},       // final loop link is not followed
		{filepath.Join(dir, "dangling"), filepath.Join(dir, "dangling")}, // nor a dangling one
	}
	for _, tt := range ok {
		got, err := safety.Canonicalize(tt.in)
		if err != nil {
			t.Errorf("Canonicalize(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Canonicalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	for _, in := range []string{
		filepath.Join(dir, "loopa", "x"),
		filepath.Join(dir, "loopa", "x", "y"),
		filepath.Join(dir, "dangling", "x"),
		dir + "/parent/../b", // ".." through a symlink: kernel and Clean disagree
	} {
		_, err := safety.Canonicalize(in)
		if got := violationRule(t, err); got != safety.RuleInvalid {
			t.Errorf("Canonicalize(%q) rule = %q, want %q", in, got, safety.RuleInvalid)
		}
	}
}

func TestWithin(t *testing.T) {
	tests := []struct {
		root, path string
		want       bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/bc", false},
		{"/a/bc", "/a/b", false},
		{"/a/b/c", "/a/b", false},
		{"/A/B", "/a/b/c", true},
		{"/a/Café", "/a/café/x", true},
		{"/a/STRASSE", "/a/straße", true},
		{"/", "/anything", true},
		{"/a/b/", "/a/b", true},
		{"a/b", "a/b", false},
		{"/a", "a", false},
	}
	for _, tt := range tests {
		if got := safety.Within(tt.root, tt.path); got != tt.want {
			t.Errorf("Within(%q, %q) = %v, want %v", tt.root, tt.path, got, tt.want)
		}
	}
}

func TestIsProtected(t *testing.T) {
	p := newTestPolicy(t, "~/Café", "/opt2/extra")
	h := p.Home()
	hp := func(s string) string { return filepath.Join(h, s) }

	protected := []string{
		"/",
		"/System/Library/x", "/usr/local/bin", "/bin/ls", "/sbin/x", "/Library/Caches/x",
		"/private/var/folders/x", "/cores/core.1", "/Applications/Xcode.app",
		"/Volumes/Data/x", "/dev/disk0", "/opt/homebrew/Cellar", "/var", "/tmp", "/etc",
		"/System", "/usr",
		"/system/library", // case-insensitive
		filepath.Dir(h),   // ancestor of home
		"/private",        // ancestor of a temp home, and a system root outside home
		hp("Documents/x"), hp("Desktop/x"), hp("Pictures/x"), hp("Movies/x"),
		hp("Music/x"), hp("Public/x"), hp("Documents"),
		hp("documents/x"), hp("DOCUMENTS/x/y"),
		hp("Library/Mobile Documents/com~apple~CloudDocs/x"),
		hp("Library/CloudStorage/Dropbox/x"), hp("Library/Mail/V10"),
		hp("Library/Messages/chat.db"), hp("Library/Photos/x"),
		hp("Library/Keychains/login.keychain-db"),
		hp("Library/Application Support/MobileSync/Backup/abc"),
		hp("Library/Application Support/macdoc/state"),
		hp(".ssh/id_ed25519"), hp(".gnupg/x"), hp(".aws/credentials"),
		hp(".config/gh/hosts.yml"), hp(".kube/config"), hp(".docker/config.json"),
		hp(".netrc"), hp(".npmrc"), hp(".pypirc"),
		hp("Library/Containers/com.apple.mail/Data"),
		hp("Library/Containers/COM.APPLE.Notes"),
		hp("code/app/.git/objects"), hp("code/app/.git"), hp("code/app/.GIT/x"),
		hp("code/app/.env"), hp("code/app/.env.local"),
		hp("code/app/.environment"), // D4: any final component starting with .env
		hp("code/app/data.sqlite"), hp("code/app/data.SQLITE3"), hp("code/app/x.db"),
		"/Users/other/x/.git/y",
		hp("Café/x"), hp("Café/x"), // extra, NFC and NFD
		"/opt2/extra/y",
	}
	for _, path := range protected {
		if got, root := p.IsProtected(path); !got {
			t.Errorf("IsProtected(%q) = false, want true", path)
		} else if root == "" {
			t.Errorf("IsProtected(%q) returned empty root", path)
		}
	}

	notProtected := []string{
		"/Systemx/a", "/usrlocal/x", "/Users/other/Library/Caches/x",
		hp("DocumentsOld/x/y"), hp("Documents2"), hp(".sshx/a"),
		hp("Library/Containers/org.example.app/x"),
		hp("Library/Containers/com.applex/x"),
		hp("Library/Caches/com.apple.dt.Xcode"),
		hp(".gradle/caches"), hp(".docker/buildx"),
		hp("code/app/.gitignore"), hp("code/app/.github/x"),
		hp("code/app/x.dbx"), hp("code/app/x.db/y"),
		hp("code/node_modules"),
		hp("Cafe/x"),
	}
	for _, path := range notProtected {
		if got, root := p.IsProtected(path); got {
			t.Errorf("IsProtected(%q) = true (%s), want false", path, root)
		}
	}

	if got, _ := p.IsProtected("relative/x"); !got {
		t.Error("IsProtected(relative) = false, want fail-closed true")
	}
}

func TestHomeException(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	if !strings.HasPrefix(h, "/private/") {
		t.Skipf("temp home %q is not under /private", h)
	}
	if got, root := p.IsProtected(filepath.Join(h, ".gradle/caches")); got {
		t.Errorf("home path protected by %q; /private must not apply inside home", root)
	}
	// Still protected outside home, including siblings of home.
	for _, path := range []string{"/private/var/x", filepath.Join(filepath.Dir(h), "sibling/x")} {
		if got, _ := p.IsProtected(path); !got {
			t.Errorf("IsProtected(%q) = false, want true", path)
		}
	}
	// Home-relative and pattern rules still apply inside home.
	for _, path := range []string{filepath.Join(h, "Documents/x"), filepath.Join(h, "a/.git/b")} {
		if got, _ := p.IsProtected(path); !got {
			t.Errorf("IsProtected(%q) = false, want true", path)
		}
	}
}

func TestRealHomeLayout(t *testing.T) {
	p, err := safety.NewPolicy("/Users/macdoc-test-nobody", nil)
	if err != nil {
		t.Fatal(err)
	}
	allow := []string{"~/**"}
	tests := []struct{ path, rule string }{
		{"/Users/macdoc-test-nobody/.gradle/caches", ""},
		{"/Users/macdoc-test-nobody/Documents/x", safety.RuleProtected},
		{"/Users/macdoc-test-nobody/.gradle", safety.RuleTooShallow},
		{"/Users", safety.RuleProtected},
		{"/Library/Caches/x", safety.RuleProtected},
		{"/Users/other/.gradle/caches", safety.RuleProtected},
		{"/Users/Shared/x/y", safety.RuleProtected},
		{"/USERS/OTHER/x/y", safety.RuleProtected},
		{"/Users/macdoc-test-nobodyx/a/b", safety.RuleProtected},
		{"/Users/MACDOC-TEST-NOBODY/.gradle/caches", ""},
		{"/nonexistent-macdoc/x/y", safety.RuleOutsideHome}, // D6
	}
	for _, tt := range tests {
		if got := violationRule(t, p.CheckTarget(tt.path, allow)); got != tt.rule {
			t.Errorf("CheckTarget(%q) rule = %q, want %q", tt.path, got, tt.rule)
		}
	}

	for _, path := range []string{"/Users/other/x", "/Users/Shared/x", "/users/OTHER/x"} {
		got, root := p.IsProtected(path)
		if !got || root != "outside home: /Users" {
			t.Errorf("IsProtected(%q) = %v, %q; want true, %q", path, got, root, "outside home: /Users")
		}
	}
}

func TestHomeSiblingsTempHome(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	parent := filepath.Dir(h)
	want := "outside home: " + parent
	for _, path := range []string{
		filepath.Join(parent, "other", "x"),
		filepath.Join(parent, "homex", "a", "b"),
		strings.ToUpper(filepath.Join(parent, "other")) + "/x",
	} {
		got, root := p.IsProtected(path)
		if !got || root != want {
			t.Errorf("IsProtected(%q) = %v, %q; want true, %q", path, got, root, want)
		}
	}
	// Inside home is not a sibling.
	if got, root := p.IsProtected(filepath.Join(h, ".gradle", "caches")); got {
		t.Errorf("home path protected by %q", root)
	}
}

func TestCheckTarget(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	hp := func(s string) string { return filepath.Join(h, s) }
	mustMkdirAll(t, hp("Documents/inner"))
	mustMkdirAll(t, hp("proj"))
	mustMkdirAll(t, hp("Library/Caches"))
	mustSymlink(t, hp("Documents"), hp("proj/docs"))
	mustSymlink(t, hp("Documents"), hp("proj/cache"))
	mustSymlink(t, hp("loop2"), hp("loop1"))
	mustSymlink(t, hp("loop1"), hp("loop2"))

	all := []string{"~/**"}
	tests := []struct {
		name  string
		path  string
		allow []string
		rule  string
	}{
		{"allowed", hp(".gradle/caches"), []string{"~/.gradle/caches"}, ""},
		{"home itself", h, all, safety.RuleTooShallow},
		{"one below home", hp(".gradle"), all, safety.RuleTooShallow},
		{"protected wins over depth", hp("Documents"), all, safety.RuleProtected},
		{"empty allow list", hp(".gradle/caches"), nil, safety.RuleNotAllowed},
		{"no match", hp(".gradle/caches"), []string{"~/.npm/*"}, safety.RuleNotAllowed},
		{"relative", "Library/Caches/x", all, safety.RuleNotAbsolute},
		{"empty", "", all, safety.RuleInvalid},
		// String concatenation, not hp(): filepath.Join would clean the "..".
		{"dotdot traversal", h + "/Library/Caches/../../Documents/x", all, safety.RuleInvalid},
		{"dotdot through symlink", h + "/proj/docs/../x", all, safety.RuleInvalid},
		{"dotdot-like names are fine", h + "/proj/..foo/foo..", []string{"~/proj/*/*"}, ""},
		{"symlinked parent into protected", hp("proj/docs/inner/x"), all, safety.RuleProtected},
		{"final symlink not followed", hp("proj/cache"), []string{"~/proj/*"}, ""},
		{"symlink loop", hp("loop1/x/y"), all, safety.RuleInvalid},
		{"system root", "/System/Library/x", []string{"/**"}, safety.RuleProtected},
		{"outside home never allowed", "/Users/someone-else/x", []string{"/Users/*/x"}, safety.RuleOutsideHome}, // D6
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.CheckTarget(tt.path, tt.allow)
			if got := violationRule(t, err); got != tt.rule {
				t.Errorf("CheckTarget(%q, %q) = %v, want rule %q", tt.path, tt.allow, err, tt.rule)
			}
		})
	}

	// safety.Violation carries the canonical path and a readable message.
	err := p.CheckTarget(h+"/Library/Caches/../../Documents/x", all)
	var v *safety.Violation
	if !errors.As(err, &v) || v.Detail != "path contains .." {
		t.Fatalf("want *safety.Violation with Detail %q, got %v", "path contains ..", err)
	}
	err = p.CheckTarget(hp("proj/docs/inner/x"), all)
	if !errors.As(err, &v) {
		t.Fatalf("want *safety.Violation, got %v", err)
	}
	if v.Path != hp("Documents/inner/x") {
		t.Errorf("Violation.Path = %q, want %q", v.Path, hp("Documents/inner/x"))
	}
	if !strings.Contains(v.Error(), "protected") || !strings.Contains(v.Error(), v.Path) {
		t.Errorf("Violation.Error() = %q", v.Error())
	}
}

func TestAllowPatterns(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	dd := "~/Library/Developer/Xcode/DerivedData/*"
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{dd, "Library/Developer/Xcode/DerivedData/MyApp-abc", true},
		{dd, "library/developer/xcode/deriveddata/MyApp-abc", true},
		{dd, "Library/Developer/Xcode/DerivedData", false},
		{dd, "Library/Developer/Xcode/DerivedData/MyApp-abc/Build", false},
		{"~/**/node_modules", "code/app/node_modules", true},
		{"~/**/node_modules", "code/node_modules", true},
		{"~/**/node_modules", "code/app/node_modules/x", false},
		{"~/**/node_modules/**", "code/app/node_modules/x/y", true},
		{"~/Library/Developer/Xcode/DerivedData/*-*", "Library/Developer/Xcode/DerivedData/MyApp-abc", true},
		{"~/Library/Developer/Xcode/DerivedData/*-*", "Library/Developer/Xcode/DerivedData/MyApp", false},
		{"~/.android/avd/*.avd/snapshots", ".android/avd/Pixel.avd/snapshots", true},
		{"~/.gradle/wrapper/dists/?.?", ".gradle/wrapper/dists/8.4", true},
		{"~/.gradle/wrapper/dists/[0-9]*", ".gradle/wrapper/dists/8.4", true},
		{"~/.gradle/wrapper/dists/[", ".gradle/wrapper/dists/[", false}, // malformed
		{"~/.cache/Café/*", ".cache/Café/x", true},
		{"~/.gradle/caches", ".gradle/caches", true},
		{"~/./.gradle/x/../caches", ".gradle/caches", true},
		{"~/**/**/caches", ".gradle/caches", true},
		{"~", ".gradle/caches", false},
		{"~/", ".gradle/caches", false},
		{"~/**", ".gradle/caches", true},
		{h + "/.gradle/*", ".gradle/caches", true},
		{".gradle/caches", ".gradle/caches", false},        // relative
		{"~other/.gradle/caches", ".gradle/caches", false}, // ~user form
		{"", ".gradle/caches", false},
		{"~/.gradle/\x00", ".gradle/caches", false},
	}
	for _, tt := range tests {
		err := p.CheckTarget(filepath.Join(h, tt.path), []string{tt.pattern})
		if got := err == nil; got != tt.want {
			t.Errorf("pattern %q path ~/%s: allowed = %v (%v), want %v", tt.pattern, tt.path, got, err, tt.want)
		}
	}
}

func TestMatchComponentsManyStars(t *testing.T) {
	pat := make([]string, 0, 64)
	for range 32 {
		pat = append(pat, "**", "a")
	}
	name := make([]string, 64)
	for i := range name {
		name[i] = "a"
	}
	name[63] = "b"
	if safety.MatchComponents(pat, name) {
		t.Error("unexpected match")
	}
}

func TestNewPolicy(t *testing.T) {
	dir := t.TempDir()
	mustMkdirAll(t, filepath.Join(dir, "realhome"))
	mustSymlink(t, filepath.Join(dir, "realhome"), filepath.Join(dir, "homelink"))
	mustSymlink(t, filepath.Join(dir, "nowhere"), filepath.Join(dir, "dangling"))

	bad := []struct {
		home  string
		extra []string
	}{
		{"", nil},
		{"relative/home", nil},
		{"/", nil},
		{"/Users", nil},
		{"/Users/", nil},
		{"/a/../Users", nil},
		{"/a\x00/b", nil},
		{filepath.Join(dir, "dangling"), nil},
		{filepath.Join(dir, "realhome"), []string{"relative"}},
		{filepath.Join(dir, "realhome"), []string{"/x\x00y"}},
		{filepath.Join(dir, "realhome"), []string{filepath.Join(dir, "dangling")}},
		{filepath.Join(dir, "realhome"), []string{filepath.Join(dir, "dangling", "x")}},
	}
	for _, tt := range bad {
		if _, err := safety.NewPolicy(tt.home, tt.extra); err == nil {
			t.Errorf("NewPolicy(%q, %q) succeeded, want error", tt.home, tt.extra)
		}
	}

	// A symlinked home resolves to its target.
	p, err := safety.NewPolicy(filepath.Join(dir, "homelink"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := safety.Canonicalize(filepath.Join(dir, "realhome"))
	if p.Home() != want {
		t.Errorf("Home() = %q, want %q", p.Home(), want)
	}
}

func TestSymlinkedProtectedRoots(t *testing.T) {
	// A protected folder that is itself a symlink is protected at both ends.
	base, err := safety.Canonicalize(filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	ext := filepath.Join(base, "external")
	mustMkdirAll(t, home)
	mustMkdirAll(t, filepath.Join(ext, "Docs"))
	mustMkdirAll(t, filepath.Join(ext, "Notes"))
	mustSymlink(t, filepath.Join(ext, "Docs"), filepath.Join(home, "Documents"))
	mustSymlink(t, filepath.Join(ext, "Notes"), filepath.Join(home, "Notes"))

	p, err := safety.NewPolicy(home, []string{"~/Notes"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(home, "Documents"),
		filepath.Join(home, "Documents", "x"),
		filepath.Join(ext, "Docs", "x"),
		filepath.Join(home, "Notes"),
		filepath.Join(ext, "Notes", "x"),
	} {
		if err := p.CheckTarget(path, []string{"/**"}); violationRule(t, err) != safety.RuleProtected {
			t.Errorf("CheckTarget(%q) = %v, want protected", path, err)
		}
	}
	if len(p.ExtraProtected) != 2 {
		t.Errorf("extraProtected = %q, want link and target", p.ExtraProtected)
	}
}
