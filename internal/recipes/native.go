package recipes

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// nativeArgForbidden are characters a native identifier may never contain.
const nativeArgForbidden = ";|&$`<>(){}*?[]'\"\\/"

// CheckNativeArg reports whether arg may replace the {id} placeholder: a
// plain identifier, not a flag, not a reserved selector word, with no shell
// or path characters.
func CheckNativeArg(arg string) error {
	if arg == "" {
		return errors.New("tool identifier is empty")
	}
	if strings.HasPrefix(arg, "-") {
		return fmt.Errorf("tool identifier %q starts with -", arg)
	}
	if slices.ContainsFunc(reservedNativeArgs, func(r string) bool { return strings.EqualFold(r, arg) }) {
		return fmt.Errorf("tool identifier %q is a reserved word the tool may treat as a selector", arg)
	}
	for _, c := range arg {
		if unicode.IsSpace(c) || unicode.IsControl(c) || strings.ContainsRune(nativeArgForbidden, c) {
			return fmt.Errorf("tool identifier %q contains %q", arg, c)
		}
	}
	return nil
}

// Substitute replaces the {id} placeholder in argv with arg after checking
// arg with CheckNativeArg.
func Substitute(argv []string, arg string) ([]string, error) {
	if len(argv) == 0 {
		return nil, nil
	}
	out := make([]string, len(argv))
	for i, a := range argv {
		if a != Placeholder {
			out[i] = a
			continue
		}
		if err := CheckNativeArg(arg); err != nil {
			return nil, err
		}
		out[i] = arg
	}
	return out, nil
}

// MatchesNative reports whether argv is exactly the recipe's native argv
// with any {id} replaced by a valid identifier.
func MatchesNative(r Recipe, argv []string) bool {
	if r.Native == nil || len(argv) != len(r.Native.Argv) {
		return false
	}
	for i, a := range r.Native.Argv {
		if a == Placeholder {
			if CheckNativeArg(argv[i]) != nil {
				return false
			}
		} else if argv[i] != a {
			return false
		}
	}
	return true
}
