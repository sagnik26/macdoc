package recipes

import (
	"fmt"
	"slices"
	"strings"
)

// MatchKinds are the ways a recipe can name the software it covers (ADR 0007).
//
//	bundle:<bundle id>     an app, e.g. bundle:com.docker.docker
//	app:<name>             an app with no readable bundle id, by its name
//	brew:<formula or cask> a Homebrew package
//	npm:<package>          a global npm package
//	tool:<name>            a program on PATH, e.g. tool:pip
var MatchKinds = []string{"bundle", "app", "brew", "npm", "tool"}

// ParseMatch splits a match entry into its kind and identity and checks both.
// A recipe with an invalid entry fails to load, like any other bad field.
func ParseMatch(s string) (kind, id string, err error) {
	kind, id, ok := strings.Cut(s, ":")
	if !ok {
		return "", "", fmt.Errorf("match %q must be kind:identity, e.g. npm:yarn", s)
	}
	if !slices.Contains(MatchKinds, kind) {
		return "", "", fmt.Errorf("match %q: unknown kind %q (use one of %s)", s, kind, strings.Join(MatchKinds, ", "))
	}
	if strings.TrimSpace(id) == "" {
		return "", "", fmt.Errorf("match %q has no identity after the colon", s)
	}
	// Bundle IDs, formulae and packages never contain spaces or slashes. App
	// names can, so only the other kinds are checked for them.
	if kind != "app" && strings.ContainsAny(id, " \t/") {
		return "", "", fmt.Errorf("match %q: the identity may not contain spaces or slashes", s)
	}
	if kind == "bundle" && !strings.Contains(id, ".") {
		return "", "", fmt.Errorf("match %q: a bundle id has dots, e.g. com.example.App", s)
	}
	return kind, id, nil
}

// matchProblems adds a problem for each invalid match entry of r.
func matchProblems(p *problems, r Recipe) {
	for _, m := range r.Match {
		if _, _, err := ParseMatch(m); err != nil {
			p.add("%v", err)
		}
	}
}

// familyOf says which toolchain a known piece of software belongs to. A
// recipe that matches it can leave its own toolchain out, and the loader
// fills it in from here. This is the one place the software-to-toolchain
// link is written down; software not listed here needs the recipe to name a
// toolchain itself.
var familyOf = map[string]string{
	"npm:npm":                          "node",
	"npm:yarn":                         "node",
	"npm:pnpm":                         "node",
	"brew:nvm":                         "node",
	"brew:ollama":                      "ai",
	"app:LM Studio":                    "ai",
	"bundle:com.docker.docker":         "docker",
	"bundle:com.apple.dt.Xcode":        "xcode",
	"bundle:com.google.android.studio": "android",
	"tool:gradle":                      "android",
	"tool:avdmanager":                  "android",
	"tool:sdkmanager":                  "android",
	"tool:pod":                         "cocoapods",
	"tool:brew":                        "homebrew",
	"tool:pip":                         "python",
	"tool:uv":                          "python",
	"tool:poetry":                      "python",
	"tool:conda":                       "python",
}

// deriveToolchain sets r.Toolchain from its match entries when the recipe
// names none, and checks that a named toolchain agrees with them. Several
// families is an error, because the recipe would belong to two groups.
func deriveToolchain(r *Recipe) error {
	var families []string
	for _, m := range r.Match {
		if f, ok := familyOf[m]; ok && !slices.Contains(families, f) {
			families = append(families, f)
		}
	}
	switch {
	case r.Toolchain != "" && len(families) > 0 && !slices.Contains(families, r.Toolchain):
		return fmt.Errorf("recipe %s: toolchain %q disagrees with its match (it belongs to %q)",
			r.ID, r.Toolchain, strings.Join(families, ", "))
	case r.Toolchain != "":
		return nil
	case len(families) == 1:
		r.Toolchain = families[0]
		return nil
	case len(families) == 0:
		return fmt.Errorf("recipe %s: toolchain is required, because none of its match entries is known to a toolchain", r.ID)
	default:
		return fmt.Errorf("recipe %s: its match entries belong to several toolchains (%s)",
			r.ID, strings.Join(families, ", "))
	}
}
