package tests

import (
	"testing"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/sagnik26/macdoc/internal/pathfold"
	"github.com/sagnik26/macdoc/internal/safety"
	"github.com/sagnik26/macdoc/internal/store"
)

func TestSameNameSpellings(t *testing.T) {
	for _, tt := range []struct{ a, b string }{
		{"Documents", "documents"},
		{"/Users/Me/Library", "/users/me/library"},
		{"Café", "Café"},     // composed vs decomposed
		{"STRASSE", "straße"}, // sharp s folds to ss
		{"file", "ﬁle"},       // the fi ligature folds to fi
		{"İstanbul", "i̇stanbul"},
		{"\u13a3", "\uab73"}, // Cherokee: the case folding library cycles between the two forms
	} {
		if pathfold.Fold(tt.a) != pathfold.Fold(tt.b) {
			t.Errorf("Fold(%q) = %q, Fold(%q) = %q; want equal", tt.a, pathfold.Fold(tt.a), tt.b, pathfold.Fold(tt.b))
		}
	}
	for _, tt := range []struct{ a, b string }{
		{"Documents", "Document"},
		{"cafe", "café"}, // an accent is a different name
		{"a", "b"},
		{"", "a"},
	} {
		if pathfold.Fold(tt.a) == pathfold.Fold(tt.b) {
			t.Errorf("Fold(%q) == Fold(%q) = %q; want different", tt.a, tt.b, pathfold.Fold(tt.a))
		}
	}
}

// Every place that compares paths used to fold them its own way: safety used
// full case folding, the store and engine used NFC plus simple lower-casing,
// and the scanner, recipes and trend used bare lower-casing. These checks
// keep the two public entry points on the one definition.
func TestPublicFoldsAgree(t *testing.T) {
	for _, s := range []string{
		"/Users/a/STRASSE", "/Users/a/Straße", "/Users/a/İstanbul",
		"/Users/a/Café", "/Users/a/Café", "/Users/a/ﬁle", "/Users/a/Doc", "",
	} {
		want := pathfold.Fold(s)
		if got := store.Fold(s); got != want {
			t.Errorf("store.Fold(%q) = %q, pathfold.Fold = %q", s, got, want)
		}
		if got := safety.Fold(s); got != want {
			t.Errorf("safety.FoldName(%q) = %q, pathfold.Fold = %q", s, got, want)
		}
	}
}

// The ASCII shortcut must give exactly what the full algorithm gives.
func TestASCIIFastPathMatchesTheFullFold(t *testing.T) {
	full := func(s string) string { return norm.NFC.String(cases.Fold().String(norm.NFC.String(s))) }
	for _, s := range []string{"", "a", "ABC", "/Users/Me/.Trash/Foo Bar.TXT", "MiXeD_123-.~"} {
		if got, want := pathfold.Fold(s), full(s); got != want {
			t.Errorf("Fold(%q) = %q, full algorithm gives %q", s, got, want)
		}
	}
}

func FuzzFoldIsIdempotent(f *testing.F) {
	for _, s := range []string{"\u13a3", "\uab73", "", "Documents", "Café", "Café", "STRASSE", "İ", "ﬁ", "ΣΣ", "a/B/ß"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) {
			t.Skip()
		}
		once := pathfold.Fold(s)
		if twice := pathfold.Fold(once); twice != once {
			t.Fatalf("Fold is not idempotent: Fold(%q) = %q, Fold of that = %q", s, once, twice)
		}
	})
}
