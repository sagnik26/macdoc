package tests

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/safety"
)

func FuzzCanonicalize(f *testing.F) {
	for _, s := range []string{
		"", "/", "/a/b", "relative", "/a/../../b", "/var/folders/x", "/tmp",
		"/etc/hosts/x", "/a\x00b", "//a//b/", "/Café", "/\xff\xfe",
		"/Users/x/Library/Caches/../../Documents/y",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		c, err := safety.Canonicalize(in)
		if err != nil {
			var v *safety.Violation
			if !errors.As(err, &v) {
				t.Fatalf("Canonicalize(%q) error %v is not a *safety.Violation", in, err)
			}
			return
		}
		if safety.HasDotDot(in) || safety.HasDotDot(c) {
			t.Fatalf("Canonicalize(%q) = %q accepted a .. component", in, c)
		}
		c2, err := safety.Canonicalize(c)
		if err != nil {
			t.Fatalf("Canonicalize(%q) = %q, then error %v", in, c, err)
		}
		if c2 != c {
			t.Fatalf("not idempotent: %q -> %q -> %q", in, c, c2)
		}
	})
}

func FuzzCheckTarget(f *testing.F) {
	home := filepath.Join(f.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(home, "Library", "Caches"), 0o700); err != nil {
		f.Fatal(err)
	}
	p, err := safety.NewPolicy(home, []string{"~/secret"})
	if err != nil {
		f.Fatal(err)
	}
	h := p.Home()
	for _, s := range [][2]string{
		{h + "/.gradle/caches", "~/.gradle/*"},
		{h + "/.gradle", "~/**"},
		{h, "~"},
		{h + "/Library/Caches/../../Documents/x", "~/**"},
		{h + "/code/app/node_modules", "~/**/node_modules"},
		{h + "/Library/Containers/com.apple.x/y", "~/Library/Containers/*/y"},
		{"/System/x", "/**"},
		{"/Users/a/b", "/Users/*/b"},
		{filepath.Dir(h) + "/other/x", "/**"},
		{h + "/a/..b/c..", "~/**"},
		{h + "/a/.git", "~/a/*"},
		{h + "/a/b.DB", "~/**"},
		{"rel", "rel"},
		{h + "/.gradle/caches/", "~/**"},
		{h + "/.gradle/caches/.", "~/**"},
		{h + "/./.gradle/./caches", "~/**"},
		{"/nonexistent-macdoc/x/y", "/**"},
		{h + "/secret/../secret/x", "~/**"},
		{h + "/Library/Containers", "~/**"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, path, pattern string) {
		err := p.CheckTarget(path, []string{pattern})
		if err != nil {
			var v *safety.Violation
			if !errors.As(err, &v) {
				t.Fatalf("CheckTarget(%q) error %v is not a *safety.Violation", path, err)
			}
			return
		}
		c, err := safety.Canonicalize(path)
		if err != nil {
			t.Fatalf("CheckTarget(%q) ok but safety.Canonicalize failed: %v", path, err)
		}
		if prot, root := p.IsProtected(c); prot {
			t.Fatalf("CheckTarget(%q) ok but %q is protected by %q", path, c, root)
		}
		if !safety.Within(h, c) {
			t.Fatalf("CheckTarget(%q) ok but %q is not within home", path, c)
		}
		if strings.HasSuffix(c, "/") || strings.HasSuffix(path, "/") {
			t.Fatalf("CheckTarget(%q) ok for a trailing slash (canonical %q)", path, c)
		}
		if len(safety.Components(c))-len(safety.Components(h)) < 2 {
			t.Fatalf("CheckTarget(%q) ok but %q violates the depth rule", path, c)
		}
		if safety.Within(filepath.Dir(h), c) && !safety.Within(h, c) {
			t.Fatalf("CheckTarget(%q) ok but %q is a sibling of home", path, c)
		}
	})
}
