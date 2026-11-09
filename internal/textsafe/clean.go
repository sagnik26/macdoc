// Package textsafe cleans text that may come from the user's disk (file,
// folder, app and project names, tool output) before a person or a model
// sees it. A name can hold any byte but "/" and NUL, so without this it could
// redraw a terminal line, hide text, or carry instructions a model would read
// but a person wouldn't see.
//
// It only ever cleans copies for display. Never feed cleaned text back into
// planning or acting: "foo\x1b[0m" cleaned is "foo", which may name a
// different folder.
package textsafe

import (
	"reflect"
	"regexp"
	"strings"
	"unicode"
)

// ansi matches CSI, OSC and other two-byte ESC sequences. What's left of a
// malformed sequence is inert once its ESC is dropped.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// Invisible reports whether r is dropped: control characters, format
// characters (bidi controls, zero-width characters, the BOM, soft hyphen,
// interlinear annotation, and the tag characters U+E0000–E007F that models
// read as ASCII), line and paragraph separators, private-use characters,
// noncharacters, and the supplementary variation selectors, which can carry
// hidden data too.
func Invisible(r rune) bool {
	switch {
	case unicode.IsControl(r),
		unicode.Is(unicode.Cf, r),
		unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r),
		unicode.Is(unicode.Co, r),
		r >= 0xFDD0 && r <= 0xFDEF, r&0xFFFE == 0xFFFE,
		r >= 0xE0100 && r <= 0xE01EF:
		return true
	}
	return false
}

// Model cleans text for an AI model (the MCP server): ANSI sequences and
// invisible characters are dropped, newlines and tabs kept. It doesn't cap
// the length; the caller does.
func Model(s string) string {
	s = ansi.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if Invisible(r) {
			return -1
		}
		return r
	}, s)
}

// Terminal cleans text for a person at a terminal: ANSI sequences and
// invisible characters are dropped, and newlines and tabs become spaces, so a
// name can't erase, recolor, hide or fake a line.
func Terminal(s string) string {
	s = ansi.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return ' '
		}
		if Invisible(r) {
			return -1
		}
		return r
	}, s)
}

// Copy returns a deep copy of v with every string in it, map keys
// included, passed through clean. Exported fields are copied deeply;
// unexported ones are copied as they are. v must not contain pointer cycles
// (result structs are trees).
func Copy[T any](v T, clean func(string) string) T {
	src := reflect.ValueOf(&v).Elem()
	dst := reflect.New(src.Type()).Elem()
	clone(dst, src, clean)
	return dst.Interface().(T)
}

// clone copies src into dst, one kind of value at a time.
func clone(dst, src reflect.Value, clean func(string) string) {
	switch src.Kind() {
	case reflect.String:
		dst.SetString(clean(src.String()))
	case reflect.Struct:
		cloneStruct(dst, src, clean)
	case reflect.Slice:
		cloneSlice(dst, src, clean)
	case reflect.Array:
		cloneArray(dst, src, clean)
	case reflect.Map:
		cloneMap(dst, src, clean)
	case reflect.Pointer:
		clonePointer(dst, src, clean)
	case reflect.Interface:
		cloneInterface(dst, src, clean)
	default:
		dst.Set(src)
	}
}

// cloneStruct copies the exported fields deeply and the rest as they are.
func cloneStruct(dst, src reflect.Value, clean func(string) string) {
	dst.Set(src)
	for i := 0; i < src.NumField(); i++ {
		if dst.Type().Field(i).IsExported() {
			clone(dst.Field(i), src.Field(i), clean)
		}
	}
}

// cloneSlice copies a slice element by element; a nil slice stays nil.
func cloneSlice(dst, src reflect.Value, clean func(string) string) {
	if src.IsNil() {
		return
	}
	s := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
	for i := 0; i < src.Len(); i++ {
		clone(s.Index(i), src.Index(i), clean)
	}
	dst.Set(s)
}

// cloneArray copies an array element by element.
func cloneArray(dst, src reflect.Value, clean func(string) string) {
	for i := 0; i < src.Len(); i++ {
		clone(dst.Index(i), src.Index(i), clean)
	}
}

// cloneMap copies keys and values, so a name used as a key is cleaned too; a
// nil map stays nil.
func cloneMap(dst, src reflect.Value, clean func(string) string) {
	if src.IsNil() {
		return
	}
	m := reflect.MakeMapWithSize(src.Type(), src.Len())
	it := src.MapRange()
	for it.Next() {
		k := reflect.New(src.Type().Key()).Elem()
		clone(k, it.Key(), clean)
		e := reflect.New(src.Type().Elem()).Elem()
		clone(e, it.Value(), clean)
		m.SetMapIndex(k, e)
	}
	dst.Set(m)
}

// clonePointer copies what a pointer points to; a nil pointer stays nil.
func clonePointer(dst, src reflect.Value, clean func(string) string) {
	if src.IsNil() {
		return
	}
	p := reflect.New(src.Type().Elem())
	clone(p.Elem(), src.Elem(), clean)
	dst.Set(p)
}

// cloneInterface copies the value inside an interface; a nil one stays nil.
func cloneInterface(dst, src reflect.Value, clean func(string) string) {
	if src.IsNil() {
		return
	}
	e := reflect.New(src.Elem().Type()).Elem()
	clone(e, src.Elem(), clean)
	dst.Set(e)
}
