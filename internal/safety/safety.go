// Package safety decides whether a filesystem path may ever be a cleanup
// target. It is the first line of defense described in docs/SAFETY_MODEL.md
// §3 and is used by the planner and, again, at execution time by the
// executor.
//
// Invariants:
//   - A path that cannot be shown to be safe is refused: a relative path is
//     reported as protected, and a path that cannot be resolved is a
//     violation, never a pass.
//   - Paths are compared by component, folded for case and Unicode, because
//     APFS volumes are case-insensitive by default.
//   - A violation names the rule that fired, and tests pin which rule it is.
package safety

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Rule names reported in a Violation.
const (
	RuleProtected   = "protected"    // inside a protected path (§3)
	RuleTooShallow  = "too-shallow"  // $HOME itself or < 2 Components below $HOME
	RuleNotAllowed  = "not-allowed"  // matches none of the recipe allow patterns
	RuleNotAbsolute = "not-absolute" // input path was relative
	RuleInvalid     = "invalid"      // empty, NUL bytes, unresolvable
	RuleOutsideHome = "outside-home" // not within $HOME; CheckTarget never accepts such a path
)

// Violation explains why a path was rejected.
type Violation struct {
	Path   string // canonical path if known, else the input
	Rule   string // one of the Rule* constants
	Detail string // human-readable reason, e.g. the protected root that matched
}

func (v *Violation) Error() string {
	return fmt.Sprintf("safety: %s rejected (%s): %s", v.Path, v.Rule, v.Detail)
}

// AppsPattern is the one allow pattern outside $HOME that CheckTarget
// accepts (ADR 0011): an app bundle directly in /Applications. A recipe that
// lists it may move such a bundle; nothing else in /Applications, and nothing
// below a bundle, is ever a target.
const AppsPattern = "/Applications/*.app"

// Policy holds the protected-path rules for one user.
type Policy struct {
	home           string   // canonical $HOME
	ExtraProtected []string // canonical extra protected paths

	appsComps  []string        // folded Components of /Applications (a test can move it)
	homeComps  []string        // folded Components of home
	containers []protectedRoot // home/Library/Containers, lexical and resolved
	roots      []protectedRoot // system, home-relative and extra roots
}

// NewPolicy builds a policy for the given home directory. home and every
// extraProtected entry must be absolute; they are canonicalized (a trailing
// "/" is ignored). "~" prefixes in extraProtected expand to home.
//
// The resolved home must have at least 2 Components and must not be within
// any system root, except strictly below /private/var/folders (temp dirs) or
// /Volumes (homes on external disks).
func NewPolicy(home string, extraProtected []string) (*Policy, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("safety: home %q is not absolute", home)
	}
	h, err := follow(tidy(home))
	if err != nil {
		return nil, fmt.Errorf("safety: home: %w", err)
	}
	hc := parts(h)
	if len(hc) < 2 {
		return nil, fmt.Errorf("safety: home %q must have at least 2 Components", h)
	}
	if err := location(h, hc); err != nil {
		return nil, err
	}

	p := &Policy{home: h, homeComps: hc, appsComps: parts("/Applications")}
	containers := filepath.Join(h, "Library", "Containers")
	p.containers = append(p.containers, root(containers, false))
	rc, err := follow(containers)
	if err != nil {
		return nil, fmt.Errorf("safety: home: %w", err)
	}
	if rc != containers {
		p.containers = append(p.containers, root(rc, false))
	}
	for _, r := range systemRoots {
		p.roots = append(p.roots, root(r, true))
	}
	for _, rel := range homeProtected {
		if err := p.addRoot(filepath.Join(h, rel)); err != nil {
			return nil, err
		}
	}
	for _, e := range extraProtected {
		e = tidy(expand(h, e))
		if !filepath.IsAbs(e) {
			return nil, fmt.Errorf("safety: protected path %q is not absolute", e)
		}
		n := len(p.roots)
		if err := p.addRoot(e); err != nil {
			return nil, err
		}
		for _, r := range p.roots[n:] {
			p.ExtraProtected = append(p.ExtraProtected, r.path)
		}
	}
	return p, nil
}

// location rejects a resolved home inside a system root, apart from
// the homeAllowedUnder exceptions.
func location(h string, hc []string) error {
	for _, a := range homeAllowedUnder {
		ac := parts(a)
		if len(hc) > len(ac) && under(ac, hc) {
			return nil
		}
	}
	for _, r := range systemRoots {
		if under(parts(r), hc) {
			return fmt.Errorf("safety: home %q is inside system path %s", h, r)
		}
	}
	return nil
}

// tidy lexically cleans a policy root (home or extra protected path) so
// a trailing "/" is accepted there. Input with a ".." component is returned
// unchanged so that Canonicalize still rejects it.
func tidy(s string) string {
	if HasDotDot(s) {
		return s
	}
	return filepath.Clean(s)
}

// addRoot protects path both as canonicalized (the symlink itself, if it is
// one) and fully resolved (where a symlink points).
func (p *Policy) addRoot(path string) error {
	c, err := Canonicalize(path)
	if err != nil {
		return fmt.Errorf("safety: protected path: %w", err)
	}
	p.roots = append(p.roots, root(c, false))
	r, err := follow(c)
	if err != nil {
		return fmt.Errorf("safety: protected path: %w", err)
	}
	if r != c {
		p.roots = append(p.roots, root(r, false))
	}
	return nil
}

// Home returns the canonical home directory of the policy.
func (p *Policy) Home() string { return p.home }

// Canonicalize returns the canonical form of an absolute path:
//   - rejects relative paths, empty paths and paths containing NUL
//   - rejects any ".." component (lexical cleaning disagrees with the kernel
//     when ".." follows a symlink)
//   - rejects a trailing "/" or a final "." component (other than "/"
//     itself): the kernel follows a final symlink for those, but the
//     canonical form would name the link
//   - cleans inner "." Components and repeated "/" lexically
//   - resolves symlinks in every existing ancestor (the final component is NOT
//     followed if it is itself a symlink; Lstat semantics)
//   - Unicode NFC-normalizes the result
//
// Case is preserved; comparisons elsewhere are case-insensitive because APFS
// volumes are case-insensitive by default.
func Canonicalize(path string) (string, error) {
	switch {
	case path == "":
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "empty path"}
	case strings.ContainsRune(path, 0):
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "path contains a NUL byte"}
	case !filepath.IsAbs(path):
		return "", &Violation{Path: path, Rule: RuleNotAbsolute, Detail: "path is relative"}
	case HasDotDot(path):
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "path contains .."}
	case path != "/" && (strings.HasSuffix(path, "/") || strings.HasSuffix(path, "/.")):
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "trailing slash or /."}
	}
	clean := filepath.Clean(path)
	if clean == "/" {
		return clean, nil
	}
	parent, err := resolve(filepath.Dir(clean))
	if err != nil {
		return "", &Violation{Path: path, Rule: RuleInvalid, Detail: "cannot resolve parent: " + err.Error()}
	}
	return norm.NFC.String(filepath.Join(parent, filepath.Base(clean))), nil
}

// Within reports whether path equals root or is a descendant of it. Both must
// already be canonical. The comparison is component-wise (so "/a/bc" is not
// within "/a/b"), Unicode NFC, and case-insensitive.
func Within(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	return under(parts(root), parts(path))
}

// CheckTarget canonicalizes path and returns nil only if it may be a cleanup
// target:
//   - it is not within any protected path (built-in list from
//     docs/SAFETY_MODEL.md §3 plus extraProtected)
//   - it does not contain a named protected path (a home-relative or
//     extraProtected root, or the ~/Library/Containers/com.apple.* entries,
//     at their lexical or resolved location)
//   - it is within $HOME (RuleOutsideHome); every macdoc target outside home
//     is acted on only through native tool commands, never by path
//   - it is not $HOME, not a top-level home folder, and is at least 2
//     Components below $HOME
//   - it matches at least one allow pattern
//
// Checks run in that order: canonicalize, protected, outside-home, depth,
// allow.
//
// Allow patterns are absolute or "~"-prefixed. "*" matches exactly one path
// component (no "/"); "**" matches zero or more Components. Matching is
// case-insensitive and NFC-normalized. An empty allow list allows nothing.
//
// On rejection the error is a *Violation.
func (p *Policy) CheckTarget(path string, allow []string) error {
	c, err := Canonicalize(path)
	if err != nil {
		return err
	}
	if p.appBundle(c, allow) {
		return nil
	}
	if prot, root := p.isProtectedWith(c, p.currentContainers()); prot {
		detail := "inside protected path " + root
		if strings.HasPrefix(root, ContainsProtected) {
			detail = root
		}
		return &Violation{Path: c, Rule: RuleProtected, Detail: detail}
	}
	pc := parts(c)
	if !under(p.homeComps, pc) {
		return &Violation{Path: c, Rule: RuleOutsideHome, Detail: "not within " + p.home}
	}
	if !p.deep(pc) {
		return &Violation{Path: c, Rule: RuleTooShallow, Detail: "must be at least 2 Components below " + p.home}
	}
	if !p.allowed(pc, allow) {
		return &Violation{Path: c, Rule: RuleNotAllowed, Detail: "matches no allow pattern"}
	}
	return nil
}

// appBundle reports whether c is "<apps folder>/<Name>.app", one level below
// the apps folder, and the recipe's allow list names AppsPattern. It is the
// only way a path outside $HOME passes CheckTarget, and it is exact: the
// parent must be the apps folder itself, so nothing deeper, nothing beside it
// and no look-alike folder qualifies.
func (p *Policy) appBundle(c string, allow []string) bool {
	if !slices.Contains(allow, AppsPattern) {
		return false
	}
	pc := parts(c)
	if len(pc) != len(p.appsComps)+1 || !under(p.appsComps, pc) {
		return false
	}
	name := pc[len(pc)-1]
	return strings.HasSuffix(name, ".app") && len(name) > len(".app")
}

// SetAppsFolder points the apps folder somewhere else. Only tests use it, so
// they can use a fixture instead of the real /Applications.
func (p *Policy) SetAppsFolder(dir string) {
	if c, err := follow(tidy(dir)); err == nil {
		dir = c
	}
	p.appsComps = parts(dir)
}

// IsProtected reports whether a canonical path is inside a protected area,
// and if so which protected root matched.
//
// IMPORTANT: path MUST be canonical, i.e. the result of Canonicalize. The
// check is purely lexical: non-canonical input (a path through a symlinked
// parent, or with "." or ".." Components) can miss a protected root that the
// kernel would resolve it to. CheckTarget canonicalizes for you.
func (p *Policy) IsProtected(path string) (bool, string) {
	return p.isProtected(path)
}
