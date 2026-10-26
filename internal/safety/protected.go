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
var systemRoots = []string{
	"/System", "/usr", "/bin", "/sbin", "/Library", "/private", "/cores",
	"/Applications", "/Volumes", "/dev", "/opt",
	"/var", "/tmp", "/etc",
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

// protectedSuffixes protect a path whose final component ends in them.
var protectedSuffixes = []string{".sqlite", ".sqlite3", ".db"}

// protectedRoot is one protected path with its folded components cached.
type protectedRoot struct {
	path   string
	comps  []string
	system bool
}

func newRoot(path string, system bool) protectedRoot {
	return protectedRoot{path: path, comps: foldedComponents(path), system: system}
}

// isProtected implements IsProtected. A path that is not absolute is reported
// as protected (fail closed).
func (p *Policy) isProtected(path string) (bool, string) {
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

	for _, c := range pc {
		if c == ".git" {
			return true, "any .git directory"
		}
	}

	if hasPrefixComps(p.containersComps, pc) && len(pc) > len(p.containersComps) &&
		strings.HasPrefix(pc[len(p.containersComps)], "com.apple.") {
		return true, filepath.Join(p.home, "Library/Containers/com.apple.*")
	}

	last := pc[len(pc)-1]
	if last == ".env" || strings.HasPrefix(last, ".env.") {
		return true, ".env files"
	}
	for _, s := range protectedSuffixes {
		if strings.HasSuffix(last, s) {
			return true, "*" + s + " files"
		}
	}
	return false, ""
}
