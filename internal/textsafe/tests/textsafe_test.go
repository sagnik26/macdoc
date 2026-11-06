package tests

import (
	"github.com/sagnik26/macdoc/internal/textsafe"
	"strings"
	"testing"
	"time"
)

func TestInvisible(t *testing.T) {
	for _, r := range []rune{
		0x1b, '\r', 0x7f, 0x85, 0x9b, // C0, DEL, C1 (NEL, 8-bit CSI)
		0x200e, 0x202e, 0x2066, 0x061c, // bidi
		0x00ad, 0x200b, 0x200c, 0x200d, 0x2060, 0x2061, 0x2064, 0xfeff, 0xfff9, 0xfffb, 0x180e, // zero-width, invisible
		0xe0001, 0xe0041, 0xe007f, // tag characters
		0x2028, 0x2029, // separators
		0xe000, 0xf8ff, 0x10fffd, // private use
		0xfdd0, 0xfffe, 0x1ffff, // noncharacters
		0xe0100, 0xe01ef, // supplementary variation selectors
	} {
		if !textsafe.Invisible(r) {
			t.Errorf("U+%04X kept", r)
		}
	}
	for _, r := range "aZ09 é漢🙂-_./~" {
		if textsafe.Invisible(r) {
			t.Errorf("%q dropped", r)
		}
	}
}

func TestForModelAndTerminal(t *testing.T) {
	in := "node_modules in x\x1b[2K\r SAFE (12 MB)? [y/N] \x1b[8m\nnext\tcol\u202e" + "\U000E0069\U000E0067"
	if got, want := textsafe.ForModel(in), "node_modules in x SAFE (12 MB)? [y/N] \nnext\tcol"; got != want {
		t.Errorf("ForModel = %q, want %q", got, want)
	}
	if got, want := textsafe.ForTerminal(in), "node_modules in x  SAFE (12 MB)? [y/N]  next col"; got != want {
		t.Errorf("ForTerminal = %q, want %q", got, want)
	}
	for in, want := range map[string]string{
		"\x1b]8;;http://x\x1b\\link": "link",
		"\x1b[2J\x1b[Hfake":          "fake",
		"ig\u200bnore":               "ignore",
		"a\x00b":                     "ab",
	} {
		if got := textsafe.ForTerminal(in); got != want {
			t.Errorf("ForTerminal(%q) = %q, want %q", in, got, want)
		}
	}
}

type inner struct {
	Name string
	keep string
}

type result struct {
	Title   string
	Kind    kind
	Items   []inner
	ByName  map[string]inner
	Ptr     *inner
	Any     any
	Array   [2]string
	When    time.Time
	Bytes   int64
	private string
	Nil     []string
}

type kind string

// textsafe.Copy cleans every exported string, deeply, and never changes the original.
func TestCopy(t *testing.T) {
	bad := "x\x1b[31my"
	when := time.Date(2026, 11, 2, 14, 2, 0, 123, time.UTC)
	src := result{Title: bad, Kind: kind(bad), Items: []inner{{Name: bad, keep: bad}}, ByName: map[string]inner{bad: {Name: bad}},
		Ptr: &inner{Name: bad}, Any: inner{Name: bad}, Array: [2]string{bad, "ok"}, When: when, Bytes: 1 << 62, private: bad}
	got := textsafe.Copy(src, textsafe.ForTerminal)
	if got.Title != "xy" || got.Kind != "xy" || got.Items[0].Name != "xy" || got.ByName["xy"].Name != "xy" ||
		got.Ptr.Name != "xy" || got.Any.(inner).Name != "xy" || got.Array != [2]string{"xy", "ok"} {
		t.Errorf("not cleaned: %+v", got)
	}
	if !got.When.Equal(when) || got.Bytes != 1<<62 || got.Nil != nil {
		t.Errorf("non-strings changed: %+v", got)
	}
	if got.Items[0].keep != bad || got.private != bad {
		t.Error("unexported fields should be copied as they are")
	}
	if src.Title != bad || src.Items[0].Name != bad || src.Ptr.Name != bad || src.ByName[bad].Name != bad {
		t.Error("Copy changed the original")
	}
	if got.Ptr == src.Ptr || &got.Items[0] == &src.Items[0] {
		t.Error("Copy shares memory with the original")
	}
	if !strings.Contains(textsafe.Copy("a\nb", textsafe.ForModel), "\n") {
		t.Error("ForModel should keep newlines")
	}
}
