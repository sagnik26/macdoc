package tests

// Red-team tests (task P0-R1). Tests whose failure message starts with "BUG:"
// demonstrate a confirmed finding (see .task/REPORT.md); every other test
// documents an attack that the implementation withstands.

import (
	"errors"
	"fmt"
	"github.com/sagnik26/macdoc/internal/safety"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// rtRule runs CheckTarget and returns the violation rule ("" when accepted).
func rtRule(t *testing.T, p *safety.Policy, path string, allow ...string) string {
	t.Helper()
	return violationRule(t, p.CheckTarget(path, allow))
}

// --- Findings ---------------------------------------------------------------

// RT-01: a target that *contains* a protected root is accepted. Acting on it
// (quarantine rename, native removal) takes the protected subtree with it.
func TestRedteamAncestorOfProtectedRoot(t *testing.T) {
	p := newTestPolicy(t, "~/work/client/contracts")
	h := p.Home()
	tests := []struct {
		path, allow, contains string
	}{
		{h + "/Library/Application Support", "~/Library/*", "MobileSync/Backup and macdoc state"},
		{h + "/Library/Application Support/MobileSync", "~/Library/Application Support/*", "MobileSync/Backup"},
		{h + "/Library/Containers", "~/Library/*", "com.apple.* containers"},
		{h + "/work/client", "~/work/*", "extra protected ~/work/client/contracts"},
	}
	for _, tt := range tests {
		if rule := rtRule(t, p, tt.path, tt.allow); rule != safety.RuleProtected {
			t.Errorf("BUG: CheckTarget(%q, %q) rule = %q, want %q: the target contains %s",
				tt.path, tt.allow, rule, safety.RuleProtected, tt.contains)
		}
	}
}

// RT-02: safety.Canonicalize cleans a trailing "/." or "/" lexically, which drops
// the kernel's "follow the final symlink" semantics. CheckTarget approves the
// link while the input string, as the kernel resolves it, names ~/Documents.
// Same class of disagreement as the ".." case that is already rejected.
func TestRedteamTrailingDotFollowsFinalSymlink(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	docs := filepath.Join(h, "Documents")
	mustMkdirAll(t, docs)
	mustMkdirAll(t, filepath.Join(h, ".cache", "x"))
	link := filepath.Join(h, ".cache", "x", "link")
	mustSymlink(t, docs, link)

	for _, in := range []string{link + "/.", link + "/"} {
		fi, err := os.Stat(in)
		if err != nil {
			t.Fatal(err)
		}
		di, _ := os.Stat(docs)
		if !os.SameFile(fi, di) {
			t.Fatalf("precondition: kernel does not resolve %q to Documents", in)
		}
		if rule := rtRule(t, p, in, "~/.cache/**"); rule == "" {
			t.Errorf("BUG: CheckTarget(%q) accepted; the kernel resolves this input to %s", in, docs)
		}
	}
}

// RT-03: SQLite write-ahead log / journal / shared-memory files are not
// protected. Deleting a -wal file loses committed transactions of the db.
func TestRedteamSQLiteSidecars(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	for _, name := range []string{
		"x.sqlite-wal", "x.sqlite-shm", "x.sqlite-journal",
		"x.sqlite3-wal", "x.db-wal", "x.db-shm", "x.db-journal",
	} {
		path := h + "/code/app/" + name
		if prot, _ := p.IsProtected(path); !prot {
			t.Errorf("BUG: %s is not protected (sidecar of a protected database)", name)
		}
	}
}

// RT-04: SAFETY_MODEL.md §3 says ".env*" files; the implementation only
// protects ".env" and ".env.*". direnv's .envrc and other common spellings
// hold the same secrets.
func TestRedteamEnvStar(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	for _, name := range []string{".envrc", ".env-local", ".env_prod", ".envrc.local", ".env~"} {
		path := h + "/code/app/" + name
		if prot, _ := p.IsProtected(path); !prot {
			t.Errorf("BUG: %s is not protected (SAFETY_MODEL.md §3: .env* files)", name)
		}
	}
}

// RT-05: the Library/Containers/com.apple.* rule compares against the lexical
// ~/Library/Containers only. Every other home-relative root is also protected
// at its resolved location; this one is not, so a relocated Containers dir
// loses the rule.
func TestRedteamSymlinkedContainers(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	real := filepath.Join(h, "Library", "ContainersReal")
	mustMkdirAll(t, filepath.Join(real, "com.apple.mail", "Data"))
	mustSymlink(t, real, filepath.Join(h, "Library", "Containers"))

	path := h + "/Library/Containers/com.apple.mail/Data/x"
	c, err := safety.Canonicalize(path)
	if err != nil {
		t.Fatal(err)
	}
	if rule := rtRule(t, p, path, "~/Library/**"); rule != safety.RuleProtected {
		t.Errorf("BUG: CheckTarget(%q) (canonical %q) rule = %q, want %q", path, c, rule, safety.RuleProtected)
	}
}

// RT-06: /.vol/<dev>/<ino> is the macOS volfs inode alias. Outside $HOME
// protection is a deny list, so the alias of ~/Documents is unprotected and a
// broad pattern accepts it.
func TestRedteamVolfsAlias(t *testing.T) {
	p := newTestPolicy(t)
	docs := filepath.Join(p.Home(), "Documents")
	mustMkdirAll(t, docs)
	var st syscall.Stat_t
	if err := syscall.Stat(docs, &st); err != nil {
		t.Fatal(err)
	}
	alias := fmt.Sprintf("/.vol/%d/%d", st.Dev, st.Ino)
	ai, err := os.Stat(alias)
	if err != nil {
		t.Skipf("volfs alias not available: %v", err)
	}
	di, _ := os.Stat(docs)
	if !os.SameFile(ai, di) {
		t.Skipf("%s is not %s", alias, docs)
	}
	if prot, _ := p.IsProtected("/.vol"); !prot {
		t.Errorf("BUG: /.vol is not protected; %s aliases %s", alias, docs)
	}
	if rule := rtRule(t, p, alias, "/**"); rule == "" {
		t.Errorf("BUG: CheckTarget(%q, \"/**\") accepted; it is %s", alias, docs)
	}
}

// RT-07: safety.NewPolicy accepts a home inside a system root, and the home
// containment exception then unprotects that system area. Only a temp dir
// (under /private/var/folders) or a /Volumes home needs the exception.
func TestRedteamCraftedHome(t *testing.T) {
	for _, home := range []string{"/System/Library", "/usr/local", "/var", "/etc", "/Library/Application Support"} {
		if _, err := os.Lstat(home); err != nil {
			continue
		}
		p, err := safety.NewPolicy(home, nil)
		if err != nil {
			continue
		}
		t.Errorf("BUG: safety.NewPolicy(%q) accepted (home %q); system root protection is lifted inside it", home, p.Home())
		probe := filepath.Join(p.Home(), "a", "b")
		if rule := rtRule(t, p, probe, filepath.Join(p.Home(), "**")); rule == "" {
			t.Logf("  CheckTarget(%q) accepted", probe)
		}
	}
}

// --- Attacks that held -------------------------------------------------------

func TestRedteamCaseVariants(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	for _, path := range []string{
		h + "/DOCUMENTS/x",
		h + "/documents/x",
		h + "/.SSH/key",
		h + "/LIBRARY/MAIL/V10",
		h + "/library/containers/COM.APPLE.mail/x",
		h + "/code/app/.GIT/HEAD",
		h + "/code/app/x.DB",
		h + "/code/app/X.SQLITE3",
		h + "/code/app/.ENV.LOCAL",
		"/SYSTEM/Library",
		"/system/library/x",
		"/APPLICATIONS/Xcode.app",
		"/PRIVATE/etc/hosts",
	} {
		if prot, _ := p.IsProtected(path); !prot {
			t.Errorf("IsProtected(%q) = false", path)
		}
	}
}

// APFS (case-insensitive) treats U+017F LATIN SMALL LETTER LONG S as "s" and
// U+212A KELVIN SIGN as "k". These were the only single-rune aliases of ASCII
// letters found by a brute force over all Unicode scalars, and full case
// folding maps both.
func TestRedteamAPFSFoldAliases(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	mustMkdirAll(t, filepath.Join(h, ".ssh"))
	mustMkdirAll(t, filepath.Join(h, ".kube"))
	for _, name := range []string{".ſsh", ".Kube", "DesKtop", "Documentſ"} {
		path := filepath.Join(h, name)
		if _, err := os.Lstat(path); err != nil && (name == ".ſsh" || name == ".Kube") {
			t.Logf("filesystem does not alias %q: %v", name, err)
		}
		if prot, _ := p.IsProtected(path + "/x"); !prot {
			t.Errorf("IsProtected(%q) = false", path+"/x")
		}
	}
}

// Compatibility forms, zero-width characters and homoglyphs name different
// files on APFS, so they correctly do not match protected names.
func TestRedteamUnicodeLookalikesAreDistinct(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	mustMkdirAll(t, filepath.Join(h, "Documents"))
	for _, name := range []string{
		"Ｄocuments",                             // full-width D
		"Docu" + string(rune(0x200d)) + "ments", // zero-width joiner
		"Dоcuments",                             // Cyrillic o
		"Documents／x",                           // full-width solidus: one component, not two
	} {
		if _, err := os.Lstat(filepath.Join(h, name)); err == nil {
			t.Errorf("filesystem aliases %q to Documents but the policy treats it as distinct", name)
			continue
		}
		if rule := rtRule(t, p, filepath.Join(h, name, "x"), "~/**"); rule != "" {
			t.Errorf("look-alike %q rejected with %q; expected accepted (it is a different file)", name, rule)
		}
	}
}

func TestRedteamNFDProtectedExtra(t *testing.T) {
	p := newTestPolicy(t, "~/Café/private") // NFC é
	h := p.Home()
	if prot, _ := p.IsProtected(h + "/Café/private/x"); !prot { // NFD é
		t.Error("NFD spelling of an NFC extra protected path is not protected")
	}
	if prot, _ := p.IsProtected(h + "/CAFÉ/PRIVATE/x"); !prot {
		t.Error("upper-case spelling of an extra protected path is not protected")
	}
}

func TestRedteamPathTricks(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	mustMkdirAll(t, filepath.Join(h, "Library", "Caches"))
	tests := []struct {
		name, path, rule string
	}{
		{"dotdot", h + "/Library/Caches/../../Documents/x", safety.RuleInvalid},
		{"dotdot trailing", h + "/.cache/x/..", safety.RuleInvalid},
		{"double slash", h + "//Documents//x", safety.RuleProtected},
		{"slash dot", h + "/./Documents/./x", safety.RuleProtected},
		{"trailing slash dot", h + "/Documents/.", safety.RuleInvalid}, // P0-W3 D2 (was safety.RuleProtected)
		{"leading double slash", "//System/Library", safety.RuleProtected},
		{"nul", h + "/.cache/x\x00/y", safety.RuleInvalid},
		{"relative", "Documents/x", safety.RuleNotAbsolute},
		{"tilde not expanded", "~/Documents/x", safety.RuleNotAbsolute},
		{"home", h, safety.RuleTooShallow},
		{"home trailing slash", h + "/", safety.RuleInvalid}, // P0-W3 D2 (was safety.RuleTooShallow)
		{"depth 1", h + "/x", safety.RuleTooShallow},
		{"depth 1 via dot", h + "/./x/.", safety.RuleInvalid}, // P0-W3 D2 (was safety.RuleTooShallow)
		{"depth 1 via inner dot", h + "/./x", safety.RuleTooShallow},
		{"newline name ok", h + "/.cache/a\nb", ""},
		{"carriage return name ok", h + "/.cache/a\rb", ""},
		{"three dots is a name", h + "/.cache/...", ""},
	}
	for _, tt := range tests {
		if rule := rtRule(t, p, tt.path, "~/**"); rule != tt.rule {
			t.Errorf("%s: CheckTarget(%q) rule = %q, want %q", tt.name, tt.path, rule, tt.rule)
		}
	}
}

func TestRedteamLongPaths(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	long := h + "/.cache/" + strings.Repeat("a/", 2000) + "x"
	longComp := h + "/.cache/" + strings.Repeat("b", 300) + "/x"
	for _, path := range []string{long, longComp} {
		done := make(chan error, 1)
		go func() { done <- p.CheckTarget(path, []string{"~/**"}) }()
		select {
		case err := <-done:
			// Either answer is safe: nothing can exist at such a path.
			t.Logf("%d-byte path: %v", len(path), err)
		case <-time.After(10 * time.Second):
			t.Fatalf("CheckTarget hung on a %d-byte path", len(path))
		}
	}
}

func TestRedteamSymlinkAttacks(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	c := filepath.Join(h, ".cache", "x")
	mustMkdirAll(t, c)
	mustMkdirAll(t, filepath.Join(h, "Documents", "sub"))

	// Chain: a -> b -> c -> ~/Documents
	mustSymlink(t, filepath.Join(h, "Documents"), filepath.Join(c, "c"))
	mustSymlink(t, filepath.Join(c, "c"), filepath.Join(c, "b"))
	mustSymlink(t, "b", filepath.Join(c, "a")) // relative hop
	// Symlink to "/".
	mustSymlink(t, "/", filepath.Join(c, "root"))
	// Relative symlink climbing out of home into /private.
	mustSymlink(t, "../../..", filepath.Join(c, "up"))
	// Loop.
	mustSymlink(t, "loop2", filepath.Join(c, "loop1"))
	mustSymlink(t, "loop1", filepath.Join(c, "loop2"))
	// Dangling link whose target would be inside Documents.
	mustSymlink(t, filepath.Join(h, "Documents", "new"), filepath.Join(c, "dangling"))
	// Case-varied link into DOCUMENTS.
	mustSymlink(t, filepath.Join(h, "DOCUMENTS"), filepath.Join(c, "upper"))

	tests := []struct {
		name, path string
		reject     bool
	}{
		{"chain", c + "/a/sub/y", true},
		{"to root", c + "/root/System/Library", true},
		{"to root, private", c + "/root/private/etc/hosts", true},
		{"climb out", c + "/up/x/y", true},
		{"loop", c + "/loop1/x", true},
		{"dangling", c + "/dangling/x", true},
		{"dangling deeper", c + "/dangling/x/y", true},
		{"upper", c + "/upper/sub", true},
		// Final component is not followed: the link itself is the target.
		{"final link", c + "/a", false},
	}
	for _, tt := range tests {
		done := make(chan string, 1)
		go func() { done <- rtRule(t, p, tt.path, "~/**") }()
		select {
		case rule := <-done:
			if tt.reject && rule == "" {
				t.Errorf("%s: CheckTarget(%q) accepted", tt.name, tt.path)
			}
			if !tt.reject && rule != "" {
				t.Errorf("%s: CheckTarget(%q) rule = %q, want accepted", tt.name, tt.path, rule)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: CheckTarget hung", tt.name)
		}
	}
}

// TOCTOU note for Phase 6: safety.Canonicalize is a point-in-time answer. A parent
// swapped for a symlink afterwards changes the canonical path; the executor
// must re-canonicalize and compare (SAFETY_MODEL.md §4.3 step 3).
func TestRedteamSymlinkSwapAfterCanonicalize(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	dir := filepath.Join(h, ".cache", "swap")
	mustMkdirAll(t, dir)
	mustMkdirAll(t, filepath.Join(h, "Documents"))
	path := dir + "/victim"
	before, err := safety.Canonicalize(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.CheckTarget(path, []string{"~/**"}); err != nil {
		t.Fatalf("before swap: %v", err)
	}
	if err := os.Rename(dir, dir+".old"); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, filepath.Join(h, "Documents"), dir)
	after, err := safety.Canonicalize(path)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatalf("canonical path unchanged after swap: %q", after)
	}
	if rule := rtRule(t, p, path, "~/**"); rule != safety.RuleProtected {
		t.Errorf("after swap: rule = %q, want %q", rule, safety.RuleProtected)
	}
}

func TestRedteamHomeException(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	tmp := filepath.Dir(filepath.Dir(h)) // .../T/TestXxx
	for _, path := range []string{
		"/private/etc/hosts",
		"/private/var/db/x",
		"/var/folders/x",
		"/tmp/x/y",
		tmp + "/other/x/y",
		filepath.Dir(h) + "/home2/x/y",
		filepath.Dir(h) + "/HOME/.cache/x", // same as home on APFS: in home, allowed
	} {
		rule := rtRule(t, p, path, "/**")
		if strings.HasSuffix(path, "/HOME/.cache/x") {
			if rule != "" {
				t.Errorf("case-variant home path %q rejected with %q", path, rule)
			}
			continue
		}
		if rule == "" {
			t.Errorf("CheckTarget(%q, /**) accepted with temp home %q", path, h)
		}
	}
}

func TestRedteamAllowPatterns(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	user := filepath.Base(h)
	tests := []struct {
		name, path, allow, rule string
	}{
		{"~/** cannot reach depth 1", h + "/.gradle", "~/**", safety.RuleTooShallow},
		{"~/* cannot reach depth 1", h + "/.gradle", "~/*", safety.RuleTooShallow},
		{"/** cannot reach /System", "/System/Library/x", "/**", safety.RuleProtected},
		{"~/** cannot reach Documents", h + "/Documents/a/b", "~/**", safety.RuleProtected},
		{"~/../ escapes to sibling, still protected", filepath.Dir(h) + "/other/a/b", "~/../*/a/b", safety.RuleProtected},
		{"~user/ is not expanded", h + "/.cache/a", "~" + user + "/.cache/a", safety.RuleNotAllowed},
		{"~user pattern with abs path", h + "/.cache/a", "~" + user, safety.RuleNotAllowed},
		{"relative pattern", h + "/.cache/a", ".cache/a", safety.RuleNotAllowed},
		{"empty pattern", h + "/.cache/a", "", safety.RuleNotAllowed},
		{"pattern NUL", h + "/.cache/a", "~/.cache/a\x00", safety.RuleNotAllowed},
		{"case mismatch matches", h + "/.Cache/A", "~/.CACHE/a", ""},
		{"NFD pattern vs NFC path", h + "/.cache/café", "~/.cache/café", ""},
		{"** at start is not absolute", h + "/.cache/x/node_modules", "**/node_modules", safety.RuleNotAllowed},
		{"** at end matches dir itself", h + "/.cache/x", "~/.cache/x/**", ""},
		{"** glued to text is a single *", h + "/.cache/a/b/c", "~/.cache/a**", safety.RuleNotAllowed},
		{"malformed glob never matches", h + "/.cache/[", "~/.cache/[", safety.RuleNotAllowed},
		{"* does not cross /", h + "/.cache/a/b", "~/.cache/*", safety.RuleNotAllowed},
	}
	for _, tt := range tests {
		if rule := rtRule(t, p, tt.path, tt.allow); rule != tt.rule {
			t.Errorf("%s: CheckTarget(%q, %q) rule = %q, want %q", tt.name, tt.path, tt.allow, rule, tt.rule)
		}
	}
}

func TestRedteamPatternRules(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	tests := []struct {
		name string
		prot bool
	}{
		{"my.git.repo/x", false},
		{"x.git/objects", false}, // bare repo: see REPORT spec ambiguity A3
		{".gitignore", false},
		{".github/workflows", false},
		{".git", true},
		{".git/HEAD", true},
		{"sub/.Git/config", true},
		{".env", true},
		{".env.local", true},
		{".ENV.Production", true},
		{".env/x", false}, // .env is a final-component rule
		{"x.DB", true},
		{"x.Sqlite", true},
		{"x.sqlite3", true},
		{"x.db.bak", false},
		{"xdb", false},
		{"data.dbx", false},
	}
	for _, tt := range tests {
		path := h + "/code/app/" + tt.name
		if prot, root := p.IsProtected(path); prot != tt.prot {
			t.Errorf("IsProtected(%q) = %v (%s), want %v", tt.name, prot, root, tt.prot)
		}
	}
}

func TestRedteamNewPolicyInputs(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	mustMkdirAll(t, home)
	for _, extra := range []string{"~user/x", "rel/x", "/a/../b", "/a\x00b"} {
		if _, err := safety.NewPolicy(home, []string{extra}); err == nil {
			t.Errorf("NewPolicy accepted extra protected %q", extra)
		}
	}
	for _, h := range []string{"/", "/Users", "//", "/./", "home", "", home + "/../x"} {
		if _, err := safety.NewPolicy(h, nil); err == nil {
			t.Errorf("NewPolicy accepted home %q", h)
		}
	}
	// "~/.." in an extra protected path is cleaned before safety.Canonicalize sees
	// it, so it protects home's parent. Fails closed; noted in the report.
	pp, err := safety.NewPolicy(home, []string{"~/../x/.."})
	if err != nil {
		t.Fatal(err)
	}
	if prot, _ := pp.IsProtected(base + "/x"); !prot {
		t.Errorf("extra \"~/../x/..\" did not protect %s", base)
	}
	p1, err := safety.NewPolicy(home+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Home() != p2.Home() {
		t.Errorf("home with trailing slash %q != %q", p1.Home(), p2.Home())
	}
	// A symlinked home is protected at both locations.
	link := filepath.Join(base, "homelink")
	mustSymlink(t, home, link)
	pl, err := safety.NewPolicy(link, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rule := rtRule(t, pl, link+"/Documents/x", "/**"); rule != safety.RuleProtected {
		t.Errorf("Documents via home symlink: rule = %q", rule)
	}
}

func TestRedteamWithin(t *testing.T) {
	tests := []struct {
		root, path string
		want       bool
	}{
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a/b/../c", false},
		{"/a/b", "/A/B/c", true},
		{"/a/b", "a/b/c", false},
		{"", "/a", false},
		{"/a/b/", "/a/b", true},
		{"/", "/anything", true},
	}
	for _, tt := range tests {
		if got := safety.Within(tt.root, tt.path); got != tt.want {
			t.Errorf("Within(%q, %q) = %v, want %v", tt.root, tt.path, got, tt.want)
		}
	}
}

func TestRedteamViolationTypes(t *testing.T) {
	p := newTestPolicy(t)
	for _, in := range []string{"", "\x00", "rel", "/a/../b", "/\xff\xfe/x"} {
		err := p.CheckTarget(in, []string{"/**"})
		var v *safety.Violation
		if err != nil && !errors.As(err, &v) {
			t.Errorf("CheckTarget(%q) error %T is not *safety.Violation", in, err)
		}
	}
}

// FuzzRedteamCheckTarget extends the corpus with the attack inputs above and
// checks invariants that must hold for every input.
func FuzzRedteamCheckTarget(f *testing.F) {
	home := filepath.Join(f.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(home, "Documents"), 0o700); err != nil {
		f.Fatal(err)
	}
	p, err := safety.NewPolicy(home, nil)
	if err != nil {
		f.Fatal(err)
	}
	h := p.Home()
	for _, s := range [][2]string{
		{h + "/Library/Application Support", "~/Library/*"},
		{h + "/Library/Containers", "~/Library/*"},
		{h + "/code/x.sqlite-wal", "~/**"},
		{h + "/code/.envrc", "~/**"},
		{h + "/Documents/.", "~/**"},
		{h + "/.ſsh/key", "~/**"},
		{h + "/.Kube/config", "~/**"},
		{h + "/Ｄocuments/x", "~/**"},
		{h + "/a\nb/c", "~/**"},
		{"/.vol/1/2", "/**"},
		{"//System//Library/", "/**"},
		{"/PRIVATE/etc", "/**"},
		{h + "/x/y", "~/../home/**"},
		{h + "/x/y", "~home/x/y"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, path, pattern string) {
		err1 := p.CheckTarget(path, []string{pattern})
		err2 := p.CheckTarget(path, []string{pattern})
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("CheckTarget(%q) not deterministic: %v vs %v", path, err1, err2)
		}
		if err1 != nil {
			return
		}
		c, _ := safety.Canonicalize(path)
		if prot, root := p.IsProtected(c); prot {
			t.Fatalf("CheckTarget(%q) ok but protected by %q", path, root)
		}
		if safety.Within(h+"/Documents", c) || safety.Within(c, h) {
			t.Fatalf("CheckTarget(%q) ok for %q", path, c)
		}
	})
}
