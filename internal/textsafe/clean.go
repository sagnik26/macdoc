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

// ForModel cleans text for an AI model (the MCP server): ANSI sequences and
// invisible characters are dropped, newlines and tabs kept. It doesn't cap
// the length; the caller does.
func ForModel(s string) string {
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

// ForTerminal cleans text for a person at a terminal: ANSI sequences and
// invisible characters are dropped, and newlines and tabs become spaces, so a
// name can't erase, recolor, hide or fake a line.
func ForTerminal(s string) string {
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
	copyValue(dst, src, clean)
	return dst.Interface().(T)
}

func copyValue(dst, src reflect.Value, clean func(string) string) {
	switch src.Kind() {
	case reflect.String:
		dst.SetString(clean(src.String()))
	case reflect.Struct:
		dst.Set(src)
		for i := 0; i < src.NumField(); i++ {
			if dst.Type().Field(i).IsExported() {
				copyValue(dst.Field(i), src.Field(i), clean)
			}
		}
	case reflect.Slice:
		if src.IsNil() {
			return
		}
		s := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		for i := 0; i < src.Len(); i++ {
			copyValue(s.Index(i), src.Index(i), clean)
		}
		dst.Set(s)
	case reflect.Array:
		for i := 0; i < src.Len(); i++ {
			copyValue(dst.Index(i), src.Index(i), clean)
		}
	case reflect.Map:
		if src.IsNil() {
			return
		}
		m := reflect.MakeMapWithSize(src.Type(), src.Len())
		it := src.MapRange()
		for it.Next() {
			k := reflect.New(src.Type().Key()).Elem()
			copyValue(k, it.Key(), clean)
			e := reflect.New(src.Type().Elem()).Elem()
			copyValue(e, it.Value(), clean)
			m.SetMapIndex(k, e)
		}
		dst.Set(m)
	case reflect.Pointer:
		if src.IsNil() {
			return
		}
		p := reflect.New(src.Type().Elem())
		copyValue(p.Elem(), src.Elem(), clean)
		dst.Set(p)
	case reflect.Interface:
		if src.IsNil() {
			return
		}
		e := reflect.New(src.Elem().Type()).Elem()
		copyValue(e, src.Elem(), clean)
		dst.Set(e)
	default:
		dst.Set(src)
	}
}
