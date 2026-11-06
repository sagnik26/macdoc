package tests

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/textsafe"
)

// Copy walks every kind of value a result struct can hold. These tests pin what
// it does for each kind, so its implementation can be restructured safely.

type leaf struct {
	Name string
	n    string // unexported: copied as it is, not cleaned
}

type tree struct {
	Title    string
	Leaf     leaf
	Leaves   []leaf
	Nil      []string
	Fixed    [2]string
	ByName   map[string]string
	NilMap   map[string]string
	Ptr      *leaf
	NilPtr   *leaf
	Any      any
	NilAny   any
	Number   int
	Nested   map[string][]leaf
	PtrSlice []*leaf
}

func upper(s string) string { return strings.ToUpper(s) }

func TestCopyCleansEveryKind(t *testing.T) {
	in := tree{
		Title:    "a",
		Leaf:     leaf{Name: "b", n: "keep"},
		Leaves:   []leaf{{Name: "c"}, {Name: "d"}},
		Fixed:    [2]string{"e", "f"},
		ByName:   map[string]string{"k": "v"},
		Ptr:      &leaf{Name: "g"},
		Any:      leaf{Name: "h"},
		Number:   7,
		Nested:   map[string][]leaf{"x": {{Name: "i"}}},
		PtrSlice: []*leaf{{Name: "j"}, nil},
	}
	got := textsafe.Copy(in, upper)

	want := tree{
		Title:    "A",
		Leaf:     leaf{Name: "B", n: "keep"},
		Leaves:   []leaf{{Name: "C"}, {Name: "D"}},
		Fixed:    [2]string{"E", "F"},
		ByName:   map[string]string{"K": "V"}, // keys are cleaned too
		Ptr:      &leaf{Name: "G"},
		Any:      leaf{Name: "H"},
		Number:   7,
		Nested:   map[string][]leaf{"X": {{Name: "I"}}},
		PtrSlice: []*leaf{{Name: "J"}, nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Copy =\n%+v\nwant\n%+v", got, want)
	}
	if got.Nil != nil || got.NilMap != nil || got.NilPtr != nil || got.NilAny != nil {
		t.Errorf("nil values must stay nil: %+v", got)
	}
}

func TestCopyLeavesTheOriginalAlone(t *testing.T) {
	in := tree{
		Title:  "a",
		Leaves: []leaf{{Name: "c"}},
		ByName: map[string]string{"k": "v"},
		Ptr:    &leaf{Name: "g"},
	}
	_ = textsafe.Copy(in, upper)
	if in.Title != "a" || in.Leaves[0].Name != "c" || in.ByName["k"] != "v" || in.Ptr.Name != "g" {
		t.Errorf("the input was changed: %+v", in)
	}
	// and the copy shares no storage with it
	out := textsafe.Copy(in, strings.ToLower)
	out.Leaves[0].Name = "changed"
	out.Ptr.Name = "changed"
	if in.Leaves[0].Name != "c" || in.Ptr.Name != "g" {
		t.Errorf("the copy shares storage with the input: %+v", in)
	}
}
