package safety

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
		c, err := Canonicalize(in)
		if err != nil {
			var v *Violation
			if !errors.As(err, &v) {
				t.Fatalf("Canonicalize(%q) error %v is not a *Violation", in, err)
			}
			return
		}
		c2, err := Canonicalize(c)
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
	p, err := NewPolicy(home, []string{"~/secret"})
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
		{h + "/a/.git", "~/a/*"},
		{h + "/a/b.DB", "~/**"},
		{"rel", "rel"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, path, pattern string) {
		err := p.CheckTarget(path, []string{pattern})
		if err != nil {
			var v *Violation
			if !errors.As(err, &v) {
				t.Fatalf("CheckTarget(%q) error %v is not a *Violation", path, err)
			}
			return
		}
		c, err := Canonicalize(path)
		if err != nil {
			t.Fatalf("CheckTarget(%q) ok but Canonicalize failed: %v", path, err)
		}
		if prot, root := p.IsProtected(c); prot {
			t.Fatalf("CheckTarget(%q) ok but %q is protected by %q", path, c, root)
		}
		if Within(h, c) && len(components(c))-len(components(h)) < 2 {
			t.Fatalf("CheckTarget(%q) ok but %q violates the depth rule", path, c)
		}
	})
}
