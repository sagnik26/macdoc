// Package pathfold is the one definition of "the same name" for paths.
//
// APFS volumes are case-insensitive by default and do not care whether an
// accented letter is stored composed or decomposed, so two spellings that
// differ only in case or normalization name the same file. Everything that
// compares paths or names (the protected-path rules, the store's index, the
// scanner's skip lists, trend lookups) must fold them the same way, or two
// parts of macdoc can disagree about whether a folder is the one that was
// asked for.
package pathfold

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Fold returns the comparison key for a path or a name: Unicode NFC, full
// case folding, then NFC again. Equal keys mean the same name. The key is
// for comparing only; never use it as a path to act on.
func Fold(s string) string {
	if ascii(s) {
		// Case folding of ASCII is lower-casing, and ASCII is already NFC.
		return strings.ToLower(s)
	}
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(s)))
}

func ascii(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
