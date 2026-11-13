package tests

import (
	"testing"

	"github.com/sagnik26/macdoc/internal/safety"
)

// ADR 0011: a bundle directly in /Applications passes only when the recipe's
// allow list names safety.AppsPattern; everything else around it stays
// protected.
func TestAppBundleException(t *testing.T) {
	p := newTestPolicy(t)
	allow := []string{safety.AppsPattern}
	for _, ok := range []string{"/Applications/Foo.app", "/Applications/My App.app", "/applications/foo.APP"} {
		if err := p.CheckTarget(ok, allow); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"/Applications", // the folder itself
		"/Applications/Utilities/Disk Utility.app", // one level too deep
		"/Applications/Foo.app/Contents",           // inside a bundle
		"/Applications/Foo.app/Contents/MacOS/Foo",
		"/Applications/notes.txt",   // not a bundle
		"/Applications/.app",        // no name
		"/Applications/Foo.app.bak", // look-alike suffix
		"/Applications/Foo.app/../Bar.app",
		"/System/Applications/Mail.app", // another apps folder
		"/Applicationsx/Foo.app",        // look-alike folder
		"/Library/Foo.app",
		"/usr/local/Foo.app",
		"/Volumes/Disk/Applications/Foo.app",
	} {
		if err := p.CheckTarget(bad, allow); err == nil {
			t.Errorf("%q was allowed", bad)
		}
	}
	// Without the pattern, even a plain bundle stays protected, and other
	// allow patterns never open /Applications.
	for _, a := range [][]string{nil, {"~/Applications/*.app"}, {"/Applications/*"}, {"/Applications/**"}} {
		if err := p.CheckTarget("/Applications/Foo.app", a); err == nil {
			t.Errorf("allow %q opened /Applications", a)
		}
	}
}

// A test can point the apps folder at a fixture, and then only that folder
// is the exception: the real /Applications is protected again.
func TestAppBundleExceptionFollowsTheFixtureFolder(t *testing.T) {
	p := newTestPolicy(t)
	dir := t.TempDir()
	p.SetAppsFolder(dir)
	allow := []string{safety.AppsPattern}
	if err := p.CheckTarget(dir+"/Foo.app", allow); err != nil {
		t.Errorf("fixture bundle refused: %v", err)
	}
	if err := p.CheckTarget("/Applications/Foo.app", allow); err == nil {
		t.Error("the real /Applications stayed open after the folder moved")
	}
}
