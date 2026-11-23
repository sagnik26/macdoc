package safety

import (
	"os"
	"strings"
	"syscall"
)

// The kinds of system location `reclaim` may clean with administrator rights
// (ADR 0014 §1). The list is code: a location not named here stays protected.
const (
	SysCaches  = "caches"      // /Library/Caches/<item>
	SysLogs    = "logs"        // /Library/Logs/<item>, /private/var/log/<item>
	SysLaunchd = "launchd"     // /Library/LaunchDaemons/<file>.plist
	SysHelpers = "helpers"     // /Library/PrivilegedHelperTools/<file>
	SysBackups = "timemachine" // /Volumes/<disk>/Backups.backupdb/<machine>/<name>.inProgress
)

// SystemKinds lists every system kind.
var SystemKinds = []string{SysCaches, SysLogs, SysLaunchd, SysHelpers, SysBackups}

// SetSystemBase moves the named system locations under dir, and stops
// requiring that a target is owned by root. Only tests use it, so they can use
// a fixture instead of the real /Library.
func (p *Policy) SetSystemBase(dir string) {
	if c, err := follow(tidy(dir)); err == nil {
		dir = c
	}
	p.sysBase, p.sysAnyUser = parts(dir), true
}

const applePrefix = "com.apple."

// CheckSystemTarget returns nil only if path is one item of the named system
// kind that macdoc may remove with administrator rights (ADR 0014 §1): exactly
// one level below the named folder (or the Time Machine layout), not a symlink,
// not an Apple item, the right sort of file, owned by root, and not inside a
// folder you protected. It deliberately accepts paths under /Library and
// /private, which CheckTarget never does; nothing else there is acceptable.
func (p *Policy) CheckSystemTarget(path, kind string) error {
	c, err := Canonicalize(path)
	if err != nil {
		return err
	}
	pc := parts(c)
	if !under(p.sysBase, pc) {
		return &Violation{Path: c, Rule: RuleNotAllowed, Detail: "not a named system location"}
	}
	rest := pc[len(p.sysBase):]
	if why := systemShape(rest, kind); why != "" {
		return &Violation{Path: c, Rule: RuleNotAllowed, Detail: why}
	}
	for _, extra := range p.ExtraProtected {
		if Within(extra, c) {
			return &Violation{Path: c, Rule: RuleProtected, Detail: "protected in your settings"}
		}
	}
	return p.systemFile(c, kind)
}

// systemFile checks what is on disk at c: not a symlink, the right sort of
// file, and owned by root (unless a test moved the base).
func (p *Policy) systemFile(c, kind string) error {
	fi, err := os.Lstat(c)
	switch {
	case err != nil:
		return &Violation{Path: c, Rule: RuleInvalid, Detail: "cannot read it: " + err.Error()}
	case fi.Mode()&os.ModeSymlink != 0:
		return &Violation{Path: c, Rule: RuleInvalid, Detail: "it is a symlink"}
	case (kind == SysLaunchd || kind == SysHelpers) && !fi.Mode().IsRegular():
		return &Violation{Path: c, Rule: RuleNotAllowed, Detail: "it is not a file"}
	case !fi.Mode().IsRegular() && !fi.IsDir():
		return &Violation{Path: c, Rule: RuleNotAllowed, Detail: "it is not a file or folder"}
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !p.sysAnyUser && (!ok || st.Uid != 0) {
		return &Violation{Path: c, Rule: RuleNotAllowed, Detail: "it is not owned by root"}
	}
	return nil
}

// layout is one allowed shape: the folded Components that must come first, then
// exactly one item. suffix, when set, must end the item's name; apple items
// (com.apple.*) are refused.
type layout struct {
	prefix []string
	suffix string
	apple  bool // true: an Apple item is allowed
}

// systemLayouts is the list of named locations, per kind (ADR 0014 §1).
var systemLayouts = map[string][]layout{
	SysCaches:  {{prefix: []string{"library", "caches"}}},
	SysLogs:    {{prefix: []string{"library", "logs"}, apple: true}, {prefix: []string{"private", "var", "log"}, apple: true}},
	SysLaunchd: {{prefix: []string{"library", "launchdaemons"}, suffix: ".plist"}},
	SysHelpers: {{prefix: []string{"library", "privilegedhelpertools"}}},
}

// matches reports whether rest is l's prefix and one item.
func (l layout) matches(rest []string) bool {
	if len(rest) != len(l.prefix)+1 {
		return false
	}
	for i, w := range l.prefix {
		if rest[i] != w {
			return false
		}
	}
	last := rest[len(rest)-1]
	return last != "" && strings.HasSuffix(last, l.suffix) && (l.apple || !strings.HasPrefix(last, applePrefix))
}

// systemShape checks the folded Components below the base against the kind's
// allowed layouts and returns why not, or "".
func systemShape(rest []string, kind string) string {
	if kind == SysBackups {
		last := ""
		if len(rest) > 0 {
			last = rest[len(rest)-1]
		}
		if len(rest) == 5 && rest[0] == "volumes" && rest[1] != "" && rest[2] == "backups.backupdb" && rest[3] != "" && strings.HasSuffix(last, ".inprogress") {
			return ""
		}
		return "not an item macdoc may remove from a system location"
	}
	layouts, ok := systemLayouts[kind]
	if !ok {
		return "unknown system kind"
	}
	for _, l := range layouts {
		if l.matches(rest) {
			return ""
		}
	}
	return "not an item macdoc may remove from a system location"
}

// SystemShapeOK reports whether path, lexically, has the layout of one item of
// the named kind in the real system locations. It reads nothing from disk, so
// the privileged runner can use it as a last check on the command it is about
// to run.
func SystemShapeOK(path, kind string) bool {
	if !strings.HasPrefix(path, "/") || HasDotDot(path) || strings.ContainsRune(path, 0) || path != strings.TrimRight(path, "/") {
		return false
	}
	return systemShape(parts(path), kind) == ""
}
