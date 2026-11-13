package safety

import (
	"path/filepath"
	"slices"
	"strings"
)

// systemRoots are protected everywhere except inside $HOME when they are an
// ancestor of $HOME (see the home containment exception).
// "/" itself is handled separately: it is protected by equality only.
//
// /var, /tmp and /etc are the macOS symlinks into /private. Paths below them
// canonicalize into /private, so listing them only protects the symlinks
// themselves.
//
// /.vol and /.file are the volfs inode aliases, which can name any file
// (including ones inside $HOME) by number. /Network, /net and /home are
// network and automount points.
var systemRoots = []string{
	"/System", "/usr", "/bin", "/sbin", "/Library", "/private", "/cores",
	"/Applications", "/Volumes", "/dev", "/opt",
	"/var", "/tmp", "/etc",
	"/.vol", "/.file", "/Network", "/net", "/home",
}

// homeAllowedUnder are the only system areas a home directory may live in:
// temp dirs (tests) and external disks. A home anywhere else inside a system
// root is rejected, because the home containment exception would lift that
// system root's protection inside home.
var homeAllowedUnder = []string{"/private/var/folders", "/Volumes"}

// SystemRoots returns a copy of the built-in system roots. They are protected
// everywhere except inside $HOME when they are an ancestor of $HOME.
func SystemRoots() []string {
	return append([]string(nil), systemRoots...)
}

// homeProtected are protected paths relative to $HOME (docs/SAFETY_MODEL.md §3).
var homeProtected = []string{
	"Documents", "Desktop", "Pictures", "Movies", "Music", "Public",
	"Library/Mobile Documents", "Library/CloudStorage", "Library/Mail",
	"Library/Messages", "Library/Photos", "Library/Keychains",
	"Library/Application Support/MobileSync/Backup",
	"Library/Application Support/macdoc",
	".ssh", ".gnupg", ".aws", ".config", ".kube", ".docker/config.json",
	".netrc", ".npmrc", ".pypirc",
}

// HomeProtected returns a copy of the home-relative protected paths
// (docs/SAFETY_MODEL.md §3), "/"-separated and without a leading "~/". The
// recipe lint uses it to reject allow patterns that could match them.
func HomeProtected() []string {
	return append([]string(nil), homeProtected...)
}

// protectedSuffixes protect a path whose final component ends in them, or in
// them followed by one of sqliteSidecars.
var protectedSuffixes = []string{".sqlite", ".sqlite3", ".db"}

// sqliteSidecars are the SQLite write-ahead log, shared-memory and rollback
// journal suffixes. Losing a -wal file loses committed transactions.
var sqliteSidecars = []string{"", "-wal", "-shm", "-journal"}

// ContainsProtected prefixes the reported root when a path is a strict
// ancestor of a named protected root.
const ContainsProtected = "contains protected path "

// protectedRoot is one protected path with its folded Components cached.
type protectedRoot struct {
	path   string
	comps  []string
	system bool
}

func root(path string, system bool) protectedRoot {
	return protectedRoot{path: path, comps: parts(path), system: system}
}

// isProtected implements IsProtected with the Containers locations resolved
// when the policy was built.
func (p *Policy) isProtected(path string) (bool, string) {
	return p.isProtectedWith(path, p.containers)
}

// currentContainers returns the policy's Containers roots plus where
// ~/Library/Containers resolves to right now, in case it (or ~/Library) became
// a symlink after the policy was built. Used by CheckTarget, which already
// touches the filesystem; IsProtected stays lexical.
func (p *Policy) currentContainers() []protectedRoot {
	r, err := follow(p.containers[0].path)
	if err != nil {
		return p.containers // e.g. a symlink loop: the lexical rule still applies
	}
	rc := parts(r)
	for _, c := range p.containers {
		if len(c.comps) == len(rc) && under(c.comps, rc) {
			return p.containers
		}
	}
	return append(append([]protectedRoot(nil), p.containers...), root(r, false))
}

// probe is the path being judged: its folded Components (never empty), whether
// it is inside $HOME, and the Containers roots to use.
type probe struct {
	pc         []string
	inHome     bool
	containers []protectedRoot
}

// rule reports whether a path is protected and, when it is, why.
type rule func(p *Policy, s probe) (string, bool)

// protectedRules are checked in order and the first match gives the reported
// reason, so the order is part of the contract (tests pin the messages).
var protectedRules = []rule{
	(*Policy).sibling,
	(*Policy).root,
	(*Policy).ancestor,
	(*Policy).enclosing,
	(*Policy).repo,
	(*Policy).apple,
	(*Policy).dotenv,
	(*Policy).sqlite,
}

// isProtectedWith implements isProtected against the given Containers roots.
// A path that is not absolute is reported as protected (fail closed).
func (p *Policy) isProtectedWith(path string, containers []protectedRoot) (bool, string) {
	if !filepath.IsAbs(path) {
		return true, "not an absolute path"
	}
	pc := parts(path)
	if len(pc) == 0 {
		return true, "/"
	}
	s := probe{pc: pc, inHome: under(p.homeComps, pc), containers: containers}
	for _, check := range protectedRules {
		if reason, ok := check(p, s); ok {
			return true, reason
		}
	}
	return false, ""
}

// sibling: siblings of $HOME (other users' homes, /Users/Shared) are never
// targets. Checked first so the reported root is the most specific one.
func (p *Policy) sibling(s probe) (string, bool) {
	parent := p.homeComps[:len(p.homeComps)-1]
	if !s.inHome && len(s.pc) > len(parent) && under(parent, s.pc) {
		return "outside home: " + filepath.Dir(p.home), true
	}
	return "", false
}

// root: a path equal to or below a protected root, except a system root that
// is an ancestor of $HOME (the home containment exception).
func (p *Policy) root(s probe) (string, bool) {
	for _, r := range p.roots {
		if !under(r.comps, s.pc) {
			continue
		}
		if r.system && s.inHome && under(r.comps, p.homeComps) {
			continue
		}
		return r.path, true
	}
	return "", false
}

// ancestor: strict ancestors of $HOME (e.g. /Users) are never targets.
func (p *Policy) ancestor(s probe) (string, bool) {
	if len(s.pc) < len(p.homeComps) && under(s.pc, p.homeComps) {
		return "ancestor of " + p.home, true
	}
	return "", false
}

// enclosing: strict ancestors of named protected roots are never targets:
// acting on one takes the protected subtree with it. $HOME itself is left to
// the depth rule; its ancestors were handled above.
func (p *Policy) enclosing(s probe) (string, bool) {
	if under(s.pc, p.homeComps) {
		return "", false
	}
	for _, r := range p.roots {
		if !r.system && len(s.pc) < len(r.comps) && under(s.pc, r.comps) {
			return ContainsProtected + r.path, true
		}
	}
	// Containers itself is a strict ancestor of the com.apple.* entries.
	for _, c := range s.containers {
		if len(s.pc) <= len(c.comps) && under(s.pc, c.comps) {
			return ContainsProtected + filepath.Join(c.path, "com.apple.*"), true
		}
	}
	return "", false
}

// repo: any .git directory, anywhere.
func (p *Policy) repo(s probe) (string, bool) {
	if slices.Contains(s.pc, ".git") {
		return "any .git directory", true
	}
	return "", false
}

// apple: every ~/Library/Containers/com.apple.* entry.
func (p *Policy) apple(s probe) (string, bool) {
	for _, c := range s.containers {
		if under(c.comps, s.pc) && len(s.pc) > len(c.comps) &&
			strings.HasPrefix(s.pc[len(c.comps)], "com.apple.") {
			return filepath.Join(c.path, "com.apple.*"), true
		}
	}
	return "", false
}

// dotenv: .env* files.
func (p *Policy) dotenv(s probe) (string, bool) {
	if strings.HasPrefix(s.pc[len(s.pc)-1], ".env") {
		return ".env* files", true
	}
	return "", false
}

// sqlite: SQLite databases and their write-ahead log, shared-memory and
// rollback journal sidecars.
func (p *Policy) sqlite(s probe) (string, bool) {
	last := s.pc[len(s.pc)-1]
	for _, suffix := range protectedSuffixes {
		for _, side := range sqliteSidecars {
			if strings.HasSuffix(last, suffix+side) {
				return "*" + suffix + side + " files", true
			}
		}
	}
	return "", false
}
