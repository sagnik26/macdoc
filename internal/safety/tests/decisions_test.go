package tests

// Tests for the P0-W3 tech-lead decisions that the
// P0-R1 red-team tests do not already cover.

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/safety"
)

// D1: strict ancestors of extra protected roots, lexical and resolved.
func TestD1AncestorsOfExtras(t *testing.T) {
	base, err := safety.Canonicalize(filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	mustMkdirAll(t, filepath.Join(home, ".store", "notes"))
	mustSymlink(t, filepath.Join(home, ".store", "notes"), filepath.Join(home, "notes"))
	p, err := safety.NewPolicy(home, []string{"~/work/client/contracts", "~/notes", "/nonexistent-macdoc-opt/a/b"})
	if err != nil {
		t.Fatal(err)
	}
	h := p.Home()
	tests := []struct{ path, root string }{
		{h + "/work", h + "/work/client/contracts"},
		{h + "/work/client", h + "/work/client/contracts"},
		{h + "/.store", h + "/.store/notes"}, // resolved form of ~/notes
		{"/nonexistent-macdoc-opt", "/nonexistent-macdoc-opt/a/b"},
		{"/nonexistent-macdoc-opt/a", "/nonexistent-macdoc-opt/a/b"},
		{h + "/Library", h + "/Library/Mobile Documents"}, // first home-relative root under ~/Library
	}
	for _, tt := range tests {
		got, root := p.IsProtected(tt.path)
		if !got || root != safety.ContainsProtected+tt.root {
			t.Errorf("IsProtected(%q) = %v, %q; want true, %q", tt.path, got, root, safety.ContainsProtected+tt.root)
		}
	}

	// CheckTarget reports the containment as is, not "inside ...".
	err = p.CheckTarget(h+"/work/client", []string{"~/**"})
	var v *safety.Violation
	if !errors.As(err, &v) || v.Rule != safety.RuleProtected || v.Detail != safety.ContainsProtected+h+"/work/client/contracts" {
		t.Errorf("CheckTarget(~/work/client) = %v; want protected, %q", err, safety.ContainsProtected+h+"/work/client/contracts")
	}

	// Siblings and descendants-of-siblings are unaffected.
	for _, path := range []string{h + "/work/other/x", h + "/work/client/contractsx/y", h + "/.store/other"} {
		if got, root := p.IsProtected(path); got {
			t.Errorf("IsProtected(%q) = true (%s), want false", path, root)
		}
	}

	// $HOME itself is left to the depth rule.
	if rule := violationRule(t, p.CheckTarget(h, []string{"~/**"})); rule != safety.RuleTooShallow {
		t.Errorf("CheckTarget(home) rule = %q, want %q", rule, safety.RuleTooShallow)
	}
}

// D1 + D5: ~/Library/Containers itself is protected, at its lexical and
// (policy-time) resolved location.
func TestD1D5ContainersResolvedAtPolicyTime(t *testing.T) {
	base, err := safety.Canonicalize(filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	real := filepath.Join(home, ".relocated", "Containers")
	mustMkdirAll(t, filepath.Join(real, "com.apple.mail"))
	mustMkdirAll(t, filepath.Join(home, "Library"))
	mustSymlink(t, real, filepath.Join(home, "Library", "Containers"))
	p, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		home + "/Library/Containers",
		real,
		home + "/.relocated",
		real + "/com.apple.mail/Data",
	} {
		if got, _ := p.IsProtected(path); !got {
			t.Errorf("IsProtected(%q) = false, want true", path)
		}
	}
	if got, root := p.IsProtected(real + "/org.example.app/x"); got {
		t.Errorf("non-Apple container protected by %q", root)
	}
}

// D5: a Containers symlink loop created after the policy does not break
// CheckTarget; the lexical rule still applies.
func TestD5ContainersLoopAfterPolicy(t *testing.T) {
	p := newTestPolicy(t)
	h := p.Home()
	mustMkdirAll(t, filepath.Join(h, "Library"))
	mustSymlink(t, filepath.Join(h, "Library", "C2"), filepath.Join(h, "Library", "Containers"))
	mustSymlink(t, filepath.Join(h, "Library", "Containers"), filepath.Join(h, "Library", "C2"))
	if err := p.CheckTarget(h+"/.cache/x/y", []string{"~/**"}); err != nil {
		t.Errorf("CheckTarget with a Containers loop: %v", err)
	}
	if rule := violationRule(t, p.CheckTarget(h+"/Library/Containers", []string{"~/**"})); rule != safety.RuleProtected {
		t.Errorf("Containers loop link itself: rule = %q, want %q", rule, safety.RuleProtected)
	}
}

// D5: safety.NewPolicy fails if ~/Library/Containers cannot be resolved.
func TestD5NewPolicyContainersLoop(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	mustMkdirAll(t, filepath.Join(home, "Library"))
	mustSymlink(t, filepath.Join(home, "Library", "C2"), filepath.Join(home, "Library", "Containers"))
	mustSymlink(t, filepath.Join(home, "Library", "Containers"), filepath.Join(home, "Library", "C2"))
	if _, err := safety.NewPolicy(home, nil); err == nil {
		t.Error("NewPolicy with a Containers symlink loop succeeded, want error")
	}
}

// D2: trailing "/" and final "." are rejected; inner ones are still cleaned.
func TestD2TrailingSlashAndDot(t *testing.T) {
	for _, in := range []string{"/a/", "/a/.", "/a/b//", "/.", "//", "/a/./"} {
		_, err := safety.Canonicalize(in)
		var v *safety.Violation
		if !errors.As(err, &v) || v.Rule != safety.RuleInvalid || v.Detail != "trailing slash or /." {
			t.Errorf("Canonicalize(%q) = %v; want invalid, trailing slash or /.", in, err)
		}
	}
	for _, tt := range []struct{ in, want string }{
		{"/", "/"},
		{"/nonexistent-macdoc/./a/./b", "/nonexistent-macdoc/a/b"},
		{"/nonexistent-macdoc//a", "/nonexistent-macdoc/a"},
		{"/nonexistent-macdoc/.a/b.", "/nonexistent-macdoc/.a/b."},
	} {
		got, err := safety.Canonicalize(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("Canonicalize(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	p := newTestPolicy(t)
	if err := p.CheckTarget(p.Home()+"/./.cache/./x", []string{"~/**"}); err != nil {
		t.Errorf("inner /./ rejected: %v", err)
	}
}

// D6: ordering (protected before outside-home) and the new system roots.
func TestD6OutsideHome(t *testing.T) {
	p := newTestPolicy(t)
	tests := []struct{ path, rule string }{
		{"/System/Library/x", safety.RuleProtected},
		{"/Applications/Xcode.app", safety.RuleProtected},
		{"/.vol/1/2", safety.RuleProtected},
		{"/.file/id=1.2", safety.RuleProtected},
		{"/Network/Servers/x", safety.RuleProtected},
		{"/net/host/x", safety.RuleProtected},
		{"/home/x/y", safety.RuleProtected},
		{"/nonexistent-macdoc/x/y", safety.RuleOutsideHome},
		{"/Users/macdoc-nobody/.cache/x", safety.RuleOutsideHome},
	}
	for _, tt := range tests {
		if rule := violationRule(t, p.CheckTarget(tt.path, []string{"/**"})); rule != tt.rule {
			t.Errorf("CheckTarget(%q, /**) rule = %q, want %q", tt.path, rule, tt.rule)
		}
	}
}

func TestD9SystemRootsCopy(t *testing.T) {
	r := safety.SystemRoots()
	for _, want := range []string{"/System", "/private", "/.vol", "/.file", "/Network", "/net", "/home"} {
		if !slices.Contains(r, want) {
			t.Errorf("SystemRoots() lacks %q", want)
		}
	}
	r[0] = "/mutated"
	if safety.SystemRoots()[0] == "/mutated" {
		t.Error("SystemRoots() returned the internal slice, want a copy")
	}
}

// D7: a home inside a system root is rejected unless strictly below
// /private/var/folders or /Volumes.
func TestD7HomeLocation(t *testing.T) {
	for _, home := range []string{
		"/usr/local/nonexistent-macdoc",
		"/System/Library/nonexistent-macdoc",
		"/Library/nonexistent-macdoc/home",
		"/private/var/folders",
		"/private/var/nonexistent-macdoc",
		"/private/tmp/nonexistent-macdoc",
		"/opt/nonexistent-macdoc",
		"/.vol/1",
	} {
		if p, err := safety.NewPolicy(home, nil); err == nil {
			t.Errorf("NewPolicy(%q) accepted (home %q)", home, p.Home())
		} else if !strings.Contains(err.Error(), "system path") && !strings.Contains(err.Error(), "home") {
			t.Errorf("NewPolicy(%q) error %v", home, err)
		}
	}

	// Lexical only: nothing under /Volumes needs to exist.
	home := "/Volumes/macdoc-nonexistent-vol/home"
	p, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatalf("NewPolicy(%q): %v", home, err)
	}
	if p.Home() != home {
		t.Fatalf("Home() = %q, want %q", p.Home(), home)
	}
	// The containment exception keeps working inside the /Volumes home...
	if err := p.CheckTarget(home+"/.cache/x", []string{"~/**"}); err != nil {
		t.Errorf("CheckTarget inside /Volumes home: %v", err)
	}
	// ...and /Volumes stays protected outside it.
	for _, path := range []string{"/Volumes/other/x", "/Volumes/macdoc-nonexistent-vol/other/x", "/Volumes"} {
		if got, _ := p.IsProtected(path); !got {
			t.Errorf("IsProtected(%q) = false with /Volumes home", path)
		}
	}
	if got, _ := p.IsProtected(home + "/Documents/x"); !got {
		t.Error("Documents in /Volumes home not protected")
	}

	// A temp-dir home (strictly below /private/var/folders) is accepted.
	tp := newTestPolicy(t)
	if !strings.HasPrefix(tp.Home(), "/private/var/folders/") {
		t.Logf("temp home %q is not under /private/var/folders", tp.Home())
	}
}
