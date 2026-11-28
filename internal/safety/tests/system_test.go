package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/safety"
)

// sysFixture builds a stand-in for the system folders under a temp dir.
func sysFixture(t *testing.T) (*safety.Policy, string) {
	t.Helper()
	p := newTestPolicy(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.SetSystemBase(base)
	for _, d := range []string{"Library/Caches/Acme", "Library/Caches/com.apple.thing", "Library/Caches-old/Acme", "Library/Logs/Acme",
		"private/var/log/old", "private/var/logs/old", "Volumes/Backup/Backups.backupdb/Mac/2026-11-01.inProgress",
		"Volumes/Backup/Backups.backupdb/Mac/2026-10-01-120000", "Library/Caches/Acme/deep"} {
		mustMkdirAll(t, filepath.Join(base, d))
	}
	for _, f := range []string{"Library/LaunchDaemons/org.acme.daemon.plist", "Library/LaunchDaemons/com.apple.x.plist",
		"Library/LaunchDaemons/org.acme.daemon.plist.bak", "Library/PrivilegedHelperTools/org.acme.helper",
		"Library/PrivilegedHelperTools/com.apple.helper", "private/var/log/system.log"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(base, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustSymlink(t, "/etc", filepath.Join(base, "Library/Caches/Link"))
	return p, base
}

// Each kind accepts exactly its own layout, one level down, and refuses
// everything else: look-alike folders, Apple items, links, deeper paths and
// another kind's items.
func TestCheckSystemTarget(t *testing.T) {
	p, b := sysFixture(t)
	ok := []struct{ kind, rel string }{
		{safety.SysCaches, "Library/Caches/Acme"},
		{safety.SysLogs, "Library/Logs/Acme"},
		{safety.SysLogs, "private/var/log/old"},
		{safety.SysLogs, "private/var/log/system.log"},
		{safety.SysLaunchd, "Library/LaunchDaemons/org.acme.daemon.plist"},
		{safety.SysHelpers, "Library/PrivilegedHelperTools/org.acme.helper"},
		{safety.SysBackups, "Volumes/Backup/Backups.backupdb/Mac/2026-11-01.inProgress"},
	}
	for _, c := range ok {
		if err := p.CheckSystemTarget(filepath.Join(b, c.rel), c.kind); err != nil {
			t.Errorf("%s %s refused: %v", c.kind, c.rel, err)
		}
	}
	bad := []struct{ why, kind, rel string }{
		{"Apple cache", safety.SysCaches, "Library/Caches/com.apple.thing"},
		{"Apple daemon", safety.SysLaunchd, "Library/LaunchDaemons/com.apple.x.plist"},
		{"Apple helper", safety.SysHelpers, "Library/PrivilegedHelperTools/com.apple.helper"},
		{"symlink", safety.SysCaches, "Library/Caches/Link"},
		{"the folder itself", safety.SysCaches, "Library/Caches"},
		{"two deep", safety.SysCaches, "Library/Caches/Acme/deep"},
		{"look-alike folder", safety.SysCaches, "Library/Caches-old/Acme"},
		{"look-alike log folder", safety.SysLogs, "private/var/logs/old"},
		{"wrong kind", safety.SysLogs, "Library/Caches/Acme"},
		{"not a plist", safety.SysLaunchd, "Library/LaunchDaemons/org.acme.daemon.plist.bak"},
		{"complete backup", safety.SysBackups, "Volumes/Backup/Backups.backupdb/Mac/2026-10-01-120000"},
		{"a folder as a daemon", safety.SysLaunchd, "Library/LaunchDaemons"},
		{"missing", safety.SysCaches, "Library/Caches/Gone"},
		{"unknown kind", "everything", "Library/Caches/Acme"},
		{"outside every location", safety.SysCaches, "System/Library/Caches/Acme"},
	}
	for _, c := range bad {
		if err := p.CheckSystemTarget(filepath.Join(b, c.rel), c.kind); err == nil {
			t.Errorf("%s: %s %s accepted", c.why, c.kind, c.rel)
		}
	}
	if err := p.CheckSystemTarget("Library/Caches/Acme", safety.SysCaches); err == nil {
		t.Error("a relative path was accepted")
	}
	if err := p.CheckSystemTarget(b+"/Library/Caches/Acme/../Acme", safety.SysCaches); err == nil {
		t.Error("a path with .. was accepted")
	}
}

// Without the test base the real /Library is named, but nothing in a fixture
// or under /System or /usr is ever accepted, and a path you protected is
// refused.
func TestCheckSystemTargetProduction(t *testing.T) {
	p := newTestPolicy(t)
	for _, path := range []string{"/System/Library/Caches/x", "/usr/local/x", "/Library", "/private/var/log", "/etc/hosts", t.TempDir()} {
		for _, k := range safety.SystemKinds {
			if err := p.CheckSystemTarget(path, k); err == nil {
				t.Errorf("%s %s accepted", k, path)
			}
		}
	}
	q, b := sysFixture(t)
	_ = q
	prot := newTestPolicy(t)
	prot.ExtraProtected = append(prot.ExtraProtected, filepath.Join(b, "Library", "Caches", "Acme"))
	prot.SetSystemBase(b)
	if err := prot.CheckSystemTarget(filepath.Join(b, "Library/Caches/Acme"), safety.SysCaches); err == nil {
		t.Error("a path protected in settings was accepted")
	}
}

// The shape check the privileged runner repeats reads nothing from disk.
func TestSystemShapeOK(t *testing.T) {
	good := map[string]string{
		"/Library/Caches/Foo": safety.SysCaches, "/Library/Logs/Foo": safety.SysLogs, "/private/var/log/foo.log": safety.SysLogs,
		"/Library/LaunchDaemons/org.a.b.plist": safety.SysLaunchd, "/Library/PrivilegedHelperTools/org.a.b": safety.SysHelpers,
		"/Volumes/Backup/Backups.backupdb/Mac/2026-11-01.inProgress": safety.SysBackups,
	}
	for path, kind := range good {
		if !safety.SystemShapeOK(path, kind) {
			t.Errorf("%s %s refused", kind, path)
		}
	}
	for _, path := range []string{"/", "/Library", "/Library/Caches", "/Library/Caches/", "/Library/Caches/a/b", "/Library/Caches/../Logs/a",
		"/Library/Caches/com.apple.a", "/etc/passwd", "Library/Caches/a", "/Library/Caches/a\x00b", "/private/var/logs/a"} {
		if safety.SystemShapeOK(path, safety.SysCaches) || safety.SystemShapeOK(path, safety.SysLogs) {
			t.Errorf("%q accepted", path)
		}
	}
}
