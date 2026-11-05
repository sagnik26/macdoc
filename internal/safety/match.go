package safety

import (
	"path"
	"path/filepath"
	"strings"
)

// patternComponents expands a "~" prefix and returns the folded Components of
// an allow pattern. ok is false for any pattern that is not absolute or
// "~"-prefixed (including "~user/...").
func (p *Policy) patternComponents(pattern string) (comps []string, ok bool) {
	if strings.ContainsRune(pattern, 0) {
		return nil, false
	}
	if pattern == "~" || strings.HasPrefix(pattern, "~/") {
		pattern = homeRelative(p.home, pattern)
	} else if !strings.HasPrefix(pattern, "/") {
		return nil, false
	}
	return foldedComponents(pattern), true
}

// matchAllow reports whether the folded path Components match any pattern.
func (p *Policy) matchAllow(pc []string, allow []string) bool {
	for _, pat := range allow {
		if comps, ok := p.patternComponents(pat); ok && MatchComponents(comps, pc) {
			return true
		}
	}
	return false
}

// MatchComponents matches pattern Components against path Components. "**"
// matches zero or more whole Components; every other pattern component is
// matched against exactly one path component with path.Match. A malformed
// pattern component never matches.
//
// It is a dynamic-programming match, so it stays O(len(pat)*len(name)) no
// matter how many "**" the pattern contains.
func MatchComponents(pat, name []string) bool {
	m := len(name)
	// next[j] reports whether pat[i+1:] matches name[j:].
	next := make([]bool, m+1)
	next[m] = true
	for i := len(pat) - 1; i >= 0; i-- {
		cur := make([]bool, m+1)
		for j := m; j >= 0; j-- {
			if pat[i] == "**" {
				cur[j] = next[j] || (j < m && cur[j+1])
				continue
			}
			if j < m && next[j+1] {
				ok, err := path.Match(pat[i], name[j])
				cur[j] = err == nil && ok
			}
		}
		next = cur
	}
	return next[0]
}

// depthOK reports whether pc, already known to be within $HOME, is at least 2
// Components below $HOME.
func (p *Policy) depthOK(pc []string) bool {
	return len(pc)-len(p.homeComps) >= 2
}

// homeRelative expands "~" or "~/..." to home. Other inputs are returned
// unchanged.
func homeRelative(home, s string) string {
	if s == "~" {
		return home
	}
	if strings.HasPrefix(s, "~/") {
		return filepath.Join(home, s[2:])
	}
	return s
}
