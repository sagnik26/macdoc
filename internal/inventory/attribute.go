package inventory

import (
	"path/filepath"
	"sort"
	"strings"
)

// MinConfidence is the lowest confidence that assigns a folder to software.
// Below it the folder is reported as unattributed rather than guessed at.
const MinConfidence = 0.6

// minNameLen stops very short names ("go", "x") from matching folders by
// accident.
const minNameLen = 3

// Folder is one kept folder or file from a scan.
type Folder struct {
	Path  string
	Bytes int64
}

// Owner says that a folder belongs to a piece of software.
type Owner struct {
	SoftwareID string
	Path       string
	Bytes      int64
	Confidence float64
	Evidence   string
}

// candidateRoots are the folders, relative to home, where macOS and tools keep
// per-program data. Only their direct children are considered, so a folder is
// never counted twice (once as a child and once inside it).
var candidateRoots = []string{
	"Library/Caches",
	"Library/Application Support",
	"Library/Preferences",
	"Library/Containers",
	"Library/Group Containers",
	"Library/Logs",
	".config",
}

// Attribute assigns each candidate folder to the software that best explains
// it. Folders with no match of at least MinConfidence are returned as
// unattributed. Folders outside the candidate places are ignored.
func Attribute(home string, sw []Software, folders []Folder) (owners []Owner, unattributed []Folder) {
	for _, f := range folders {
		name, root, ok := candidate(home, f.Path)
		if !ok {
			continue
		}
		best, found := bestMatch(sw, name, root, f.Path)
		if !found {
			unattributed = append(unattributed, f)
			continue
		}
		owners = append(owners, Owner{
			SoftwareID: best.SoftwareID,
			Path:       f.Path,
			Bytes:      f.Bytes,
			Confidence: best.Confidence,
			Evidence:   best.Evidence,
		})
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].Path < owners[j].Path })
	sort.Slice(unattributed, func(i, j int) bool { return unattributed[i].Path < unattributed[j].Path })
	return owners, unattributed
}

// candidate reports whether p is a direct child of a candidate root under
// home, and returns its name with any ".plist" or leading dot removed.
func candidate(home, p string) (name, root string, ok bool) {
	dir, base := filepath.Split(filepath.Clean(p))
	dir = filepath.Clean(dir)
	for _, r := range candidateRoots {
		if dir == filepath.Join(home, r) {
			return strings.TrimSuffix(base, ".plist"), r, true
		}
	}
	// Dot-folders directly in home (~/.npm, ~/.cargo) are candidates too.
	if dir == filepath.Clean(home) && strings.HasPrefix(base, ".") && len(base) > 1 {
		return strings.TrimPrefix(base, "."), ".", true
	}
	return "", "", false
}

// match is one way a folder can belong to software.
type match struct {
	SoftwareID string
	Confidence float64
	Evidence   string
}

// bestMatch returns the strongest match for a folder. Ties go to the lower
// software ID, so the answer never depends on the order of the input.
func bestMatch(sw []Software, name, root, path string) (match, bool) {
	var best match
	found := false
	norm := normalize(name)
	for _, s := range sw {
		m, ok := matchOne(s, name, norm, root, path)
		if !ok {
			continue
		}
		if !found || m.Confidence > best.Confidence || (m.Confidence == best.Confidence && m.SoftwareID < best.SoftwareID) {
			best, found = m, true
		}
	}
	return best, found && best.Confidence >= MinConfidence
}

func matchOne(s Software, name, norm, root, path string) (match, bool) {
	rel := filepath.Base(path)
	if s.BundleID != "" {
		switch {
		case strings.EqualFold(name, s.BundleID):
			return match{s.ID, 0.9, "bundle id " + s.BundleID + " is the name of " + rel}, true
		case root == "Library/Group Containers" && strings.Contains(strings.ToLower(name), strings.ToLower(s.BundleID)):
			return match{s.ID, 0.8, "bundle id " + s.BundleID + " is part of the group container " + rel}, true
		}
	}
	if len(normalize(s.Name)) >= minNameLen && normalize(s.Name) == norm {
		return match{s.ID, 0.6, "name " + s.Name + " matches " + rel}, true
	}
	return match{}, false
}

// normalize lower-cases a name and keeps only letters and digits, so
// "Visual Studio Code" and "visual-studio-code" compare equal.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
