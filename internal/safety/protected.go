package safety

import (
	"path/filepath"
	"strings"
)

// systemRoots are protected everywhere except inside $HOME when they are an
// ancestor of $HOME (see the home containment exception in docs/tasks/P0-W1.md).
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

func newRoot(path string, system bool) protectedRoot {
	return protectedRoot{path: path, comps: foldedComponents(path), system: system}
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
	r, err := resolveFull(p.containers[0].path)
	if err != nil {
		return p.containers // e.g. a symlink loop: the lexical rule still applies
	}
	rc := foldedComponents(r)
	for _, c := range p.containers {
		if len(c.comps) == len(rc) && hasPrefixComps(c.comps, rc) {
			return p.containers
		}
	}
	return append(append([]protectedRoot(nil), p.containers...), newRoot(r, false))
}

// isProtectedWith implements isProtected against the given Containers roots.
// A path that is not absolute is reported as protected (fail closed).
func (p *Policy) isProtectedWith(path string, containers []protectedRoot) (bool, string) {
	if !filepath.IsAbs(path) {
		return true, "not an absolute path"
	}
	pc := foldedComponents(path)
	if len(pc) == 0 {
		return true, "/"
	}

	inHome := hasPrefixComps(p.homeComps, pc)

	// Siblings of $HOME (other users' homes, /Users/Shared) are never targets.
	// Checked first so the reported root is the most specific one.
	parent := p.homeComps[:len(p.homeComps)-1]
	if !inHome && len(pc) > len(parent) && hasPrefixComps(parent, pc) {
		return true, "outside home: " + filepath.Dir(p.home)
	}

	for _, r := range p.roots {
		if !hasPrefixComps(r.comps, pc) {
			continue
		}
		if r.system && inHome && hasPrefixComps(r.comps, p.homeComps) {
			continue // home containment exception
		}
		return true, r.path
	}

	// Strict ancestors of $HOME (e.g. /Users) are never targets.
	if len(pc) < len(p.homeComps) && hasPrefixComps(pc, p.homeComps) {
		return true, "ancestor of " + p.home
	}

	// Strict ancestors of named protected roots are never targets: acting on
	// one takes the protected subtree with it. $HOME itself is left to the
	// depth rule; its ancestors were handled above.
	if !hasPrefixComps(pc, p.homeComps) {
		for _, r := range p.roots {
			if !r.system && len(pc) < len(r.comps) && hasPrefixComps(pc, r.comps) {
				return true, ContainsProtected + r.path
			}
		}
		// Containers itself is a strict ancestor of the com.apple.* entries.
		for _, c := range containers {
			if len(pc) <= len(c.comps) && hasPrefixComps(pc, c.comps) {
				return true, ContainsProtected + filepath.Join(c.path, "com.apple.*")
			}
		}
	}

	for _, c := range pc {
		if c == ".git" {
			return true, "any .git directory"
		}
	}

	for _, c := range containers {
		if hasPrefixComps(c.comps, pc) && len(pc) > len(c.comps) &&
			strings.HasPrefix(pc[len(c.comps)], "com.apple.") {
			return true, filepath.Join(c.path, "com.apple.*")
		}
	}

	last := pc[len(pc)-1]
	if strings.HasPrefix(last, ".env") {
		return true, ".env* files"
	}
	for _, s := range protectedSuffixes {
		for _, side := range sqliteSidecars {
			if strings.HasSuffix(last, s+side) {
				return true, "*" + s + side + " files"
			}
		}
	}
	return false, ""
}
