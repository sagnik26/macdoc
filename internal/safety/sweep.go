package safety

import (
	"path/filepath"
	"strings"
)

// DSStoreName is the one file name macdoc may sweep from protected folders
// (ADR 0014 §4). The match is exact: ".DS_Store.bak" and "._.DS_Store" are not it.
const DSStoreName = ".DS_Store"

// CheckSweepRoot returns nil only if path may be the root of a .DS_Store
// sweep (ADR 0014 §4): a folder directly in $HOME, not hidden, not Library,
// and not inside a path the person protected in settings. Unlike
// CheckTarget it accepts the built-in protected folders (Documents, Desktop,
// Pictures ...), because the sweep removes one exact file name and nothing
// else; it never accepts $HOME itself, a deeper folder or a path outside it.
func (p *Policy) CheckSweepRoot(path string) error {
	c, err := Canonicalize(path)
	if err != nil {
		return err
	}
	pc := parts(c)
	if !under(p.homeComps, pc) || len(pc) != len(p.homeComps)+1 {
		return &Violation{Path: c, Rule: RuleNotAllowed, Detail: "a sweep starts at a folder directly in " + p.home}
	}
	base := filepath.Base(c)
	if strings.HasPrefix(base, ".") || strings.EqualFold(base, "Library") {
		return &Violation{Path: c, Rule: RuleProtected, Detail: base + " is never swept"}
	}
	if p.SweepSkips(c) {
		return &Violation{Path: c, Rule: RuleProtected, Detail: "protected in your settings"}
	}
	return nil
}

// SweepSkips reports whether a sweep must leave path (and everything below
// it) alone because it is, or is inside, a folder the person protected in
// settings. Configured protection can only add to what is skipped.
func (p *Policy) SweepSkips(path string) bool {
	c, err := Canonicalize(path)
	if err != nil {
		return true
	}
	for _, extra := range p.ExtraProtected {
		if Within(extra, c) {
			return true
		}
	}
	return false
}

// SweepSkipsDir reports whether a sweep never enters a folder with this base
// name: Git folders, dependency folders and media libraries (ADR 0014 §4).
func SweepSkipsDir(name string) bool {
	l := strings.ToLower(name)
	switch {
	case l == ".git", l == "node_modules", l == ".trash":
		return true
	case strings.HasSuffix(l, ".photoslibrary"), strings.HasSuffix(l, ".musiclibrary"):
		return true
	}
	return false
}
