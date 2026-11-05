package recipes

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"macdoc/internal/model"
	"macdoc/internal/safety"
)

//go:embed catalog
var catalogFS embed.FS

// Catalog is the validated set of recipes, sorted by ID.
type Catalog struct {
	Recipes []Recipe
}

// Get returns the recipe with the given ID.
func (c *Catalog) Get(id string) (Recipe, bool) {
	for _, r := range c.Recipes {
		if r.ID == id {
			return r, true
		}
	}
	return Recipe{}, false
}

// Load parses and validates the embedded catalog. Any invalid recipe makes
// Load fail: a bad recipe is a bug, never something to skip silently.
func Load() (*Catalog, error) { return LoadFS(catalogFS, "catalog") }

// LoadFS parses and validates every *.yaml file under dir in fsys. Each file
// holds a YAML list of recipes. Unknown YAML fields are errors.
func LoadFS(fsys fs.FS, dir string) (*Catalog, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	var all []Recipe
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, err
		}
		var rs []Recipe
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&rs); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("recipes: %s: %w", f, err)
		}
		all = append(all, rs...)
	}
	var errs []error
	seen := map[string]bool{}
	for _, r := range all {
		if seen[r.ID] {
			errs = append(errs, fmt.Errorf("recipe %s: duplicate id", r.ID))
		}
		seen[r.ID] = true
		if err := Validate(r); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	slices.SortFunc(all, func(a, b Recipe) int { return strings.Compare(a.ID, b.ID) })
	return &Catalog{Recipes: all}, nil
}

// NativeArgvAllowed is the exact allow-list of native argvs
// (docs/SAFETY_MODEL.md §5: recipes pin exact argv). "{id}" is literal here
// and stands for Finding.NativeArg. Anything else is rejected, so no recipe
// can run code through a tool, touch Docker volumes or wipe everything a tool
// owns.
var NativeArgvAllowed = [][]string{
	{"brew", "cleanup", "--prune=all"},
	{"brew", "autoremove"},
	{"docker", "builder", "prune", "-f"},
	{"docker", "image", "prune", "-f"},
	{"docker", "image", "prune", "-a", "-f"},
	{"docker", "container", "prune", "-f"},
	{"xcrun", "simctl", "delete", "unavailable"},
	{"xcrun", "simctl", "delete", Placeholder},
	{"xcrun", "simctl", "runtime", "delete", Placeholder},
	{"npm", "cache", "clean", "--force"},
	{"pnpm", "store", "prune"},
	{"yarn", "cache", "clean"},
	{"pip3", "cache", "purge"},
	{"uv", "cache", "clean"},
	{"conda", "clean", "--all", "-y"},
	{"ollama", "rm", Placeholder},
	{"pod", "cache", "clean", "--all"},
	{"avdmanager", "delete", "avd", "-n", Placeholder},
}

// DryRunArgvAllowed is the exact allow-list of dry_run_argv values. Detectors
// run these during scan and wins, so each must be the tool's own read-only
// dry run. None takes "{id}".
var DryRunArgvAllowed = [][]string{
	{"brew", "cleanup", "-n", "--prune=all"},
	{"brew", "autoremove", "-n"},
	{"conda", "clean", "--all", "--dry-run"},
}

// ArgvIn reports whether argv is exactly one of set.
func ArgvIn(argv []string, set [][]string) bool {
	return slices.ContainsFunc(set, func(a []string) bool { return slices.Equal(a, argv) })
}

// Validate checks one recipe, including the safety lint from the P0-R1/P0-W3
// carry-overs. It returns all problems joined.
func Validate(r Recipe) error {
	var errs []error
	bad := func(format string, a ...any) {
		errs = append(errs, fmt.Errorf("recipe %s: "+format, append([]any{r.ID}, a...)...))
	}
	if r.ID == "" || strings.ContainsAny(r.ID, " /") || !strings.Contains(r.ID, ".") {
		bad("id must be dotted with no spaces or slashes")
	}
	if r.Title == "" || r.Explain == "" {
		bad("title and explain are required")
	}
	for _, s := range r.Steps {
		if strings.TrimSpace(s) == "" || strings.Count(s, "`")%2 != 0 {
			bad("steps must be non-empty with paired backticks")
		}
	}
	if !slices.Contains(Categories, r.Category) {
		bad("unknown category %q", r.Category)
	}
	if !slices.Contains(Toolchains, r.Toolchain) {
		bad("unknown toolchain %q", r.Toolchain)
	}
	switch r.Risk {
	case model.RiskSafe, model.RiskCaution, model.RiskRisky, model.RiskAdvise:
	default:
		bad("risk must be SAFE, CAUTION, RISKY or ADVISE (BLOCKED things are not recipes)")
	}
	if _, ok := Lookup(r.Detector); !ok {
		bad("unknown detector %q", r.Detector)
	}
	switch r.Restore.Kind {
	case model.RestoreRebuild, model.RestoreRedownload, model.RestoreIrreplaceable, model.RestoreNone:
	default:
		bad("restore.kind must be rebuild, redownload, irreplaceable or none")
	}
	if r.StaleDays < 0 {
		bad("stale_days must be >= 0")
	}
	if v, ok := r.Params["dirty_risk"]; ok && !slices.Contains([]model.Risk{model.RiskCaution, model.RiskRisky}, model.Risk(v)) {
		bad("params.dirty_risk must be CAUTION or RISKY")
	}

	switch r.Method {
	case model.MethodQuarantine:
		if len(r.Allow) == 0 {
			bad("quarantine recipes need allow patterns")
		}
		if r.Native != nil {
			bad("quarantine recipes must not have native")
		}
		if r.Restore.Kind == model.RestoreIrreplaceable {
			bad("irreplaceable data must be advise-only")
		}
	case model.MethodNative:
		if r.Native == nil || len(r.Native.Argv) == 0 {
			bad("native recipes need native.argv")
		}
		if len(r.Allow) != 0 {
			bad("native recipes must not have allow patterns")
		}
	case model.MethodAdvise:
		if r.Native != nil || len(r.Allow) != 0 {
			bad("advise recipes must not have native or allow")
		}
		if r.Risk != model.RiskAdvise {
			bad("advise recipes must have risk ADVISE")
		}
	default:
		bad("method must be native, quarantine or advise")
	}
	if r.Risk == model.RiskAdvise && r.Method != model.MethodAdvise {
		bad("risk ADVISE requires method advise")
	}

	for _, p := range r.Allow {
		if err := lintPattern(p); err != nil {
			bad("allow %q: %v", p, err)
		} else if err := LintAllowProtected(p); err != nil {
			bad("allow %q: %v", p, err)
		}
	}
	for _, p := range r.KeepChildrenOf {
		if err := lintPattern(p); err != nil || strings.Contains(p, "*") {
			bad("keep_children_of %q must be a literal ~-relative directory at least 2 deep", p)
		}
	}
	for _, n := range r.KeepNames {
		if n == "" || strings.ContainsAny(n, "/*?[") || n == "." || n == ".." {
			bad("keep_names %q must be a plain directory name", n)
		}
	}
	if r.Native != nil {
		for _, argv := range [][]string{r.Native.Argv, r.Native.DryRunArgv} {
			if err := lintArgv(argv); err != nil {
				bad("native argv %q: %v", argv, err)
			}
		}
		if len(r.Native.Argv) > 0 && !ArgvIn(r.Native.Argv, NativeArgvAllowed) {
			bad("native argv %q: native argv not in the allow-list", r.Native.Argv)
		}
		if len(r.Native.DryRunArgv) > 0 && !ArgvIn(r.Native.DryRunArgv, DryRunArgvAllowed) {
			bad("native dry_run_argv %q: dry-run argv not in the allow-list", r.Native.DryRunArgv)
		}
	}
	for _, pc := range r.Preconditions {
		if !validPrecondition(pc) {
			bad("unknown precondition %q", pc)
		}
	}
	return errors.Join(errs...)
}

// lintPattern enforces: "~/"-relative (targets outside home are native-only),
// no "." or ".." components, no trailing "/", and the shallowest possible
// match at least 2 components below home ("**" may match zero components).
func lintPattern(p string) error {
	if !strings.HasPrefix(p, "~/") {
		return errors.New("must start with ~/")
	}
	if strings.HasSuffix(p, "/") || strings.Contains(p, "//") {
		return errors.New("no trailing or double slash")
	}
	minDepth := 0
	for _, c := range strings.Split(p[2:], "/") {
		switch c {
		case ".", "..", "":
			return errors.New(`no "." or ".." components`)
		case "**":
			// matches zero or more components
		default:
			if _, err := path.Match(c, ""); err != nil {
				return fmt.Errorf("bad glob component %q", c)
			}
			minDepth++
		}
	}
	if minDepth < 2 {
		return errors.New("could match fewer than 2 components below home")
	}
	return nil
}

// projectTreePatterns are the allow patterns for project folders anywhere in
// home (Phase 4 project artifacts). Their leading "~/*/**" can match inside protected folders such as
// ~/Documents, so they are exempt from the protected-root and
// two-wildcard rules of LintAllowProtected, and only from those. ProjectDirs
// skips protected folders, and CheckTarget rejects them again at plan and
// apply time.
var projectTreePatterns = []string{
	"~/*/**/node_modules", "~/*/**/.venv", "~/*/**/.next",
	"~/*/**/ios/Pods", "~/*/**/Pods", "~/*/**/ios/build",
	"~/*/**/android/build", "~/*/**/android/app/build", "~/*/**/android/.gradle",
}

// protectedNameSamples are names a final pattern component must not be able
// to match: what IsProtected protects by name anywhere in home.
var protectedNameSamples = []string{".git", ".env", ".env.local", "x.sqlite", "x.sqlite3", "x.db", "x.db-wal"}

// appleContainers stands for every ~/Library/Containers/com.apple.* entry.
const appleContainers = "library/containers/com.apple.*"

// LintAllowProtected rejects an allow pattern that could match a protected
// path (docs/SAFETY_MODEL.md §5): one whose leading components could match a
// home-relative protected root, or a folder inside or containing one; one
// whose first two components below "~" are both wildcards; or one whose final
// component could match a protected name. Matching is case-insensitive. A
// final "*" in a pattern without "**" is the children of one fixed folder
// and is left to CheckTarget, which rejects protected names at runtime.
func LintAllowProtected(p string) error {
	comps := strings.Split(strings.ToLower(strings.TrimPrefix(p, "~/")), "/")
	if !slices.Contains(projectTreePatterns, p) {
		if len(comps) >= 2 && isGlob(comps[0]) && isGlob(comps[1]) {
			return errors.New("first two components below ~ are both wildcards")
		}
		for _, root := range append(safety.HomeProtected(), appleContainers) {
			if couldOverlap(comps, strings.Split(strings.ToLower(root), "/")) {
				return fmt.Errorf("could match protected path ~/%s, or a folder inside or containing it", root)
			}
		}
	}
	last := comps[len(comps)-1]
	if last == "*" && !slices.Contains(comps, "**") {
		return nil
	}
	for _, n := range protectedNameSamples {
		if ok, _ := path.Match(last, n); ok {
			return fmt.Errorf("final component could match protected name %q", n)
		}
	}
	return nil
}

func isGlob(c string) bool { return strings.ContainsAny(c, `*?[\`) }

// couldOverlap reports whether a pattern (lowercased components below home)
// could match the protected root, a descendant or an ancestor of it: every
// component both have could match, or a "**" comes before the root ends.
func couldOverlap(pat, root []string) bool {
	for i := 0; i < len(pat) && i < len(root); i++ {
		if pat[i] == "**" {
			return true
		}
		if !componentCouldMatch(pat[i], root[i]) {
			return false
		}
	}
	return true
}

// componentCouldMatch reports whether pattern component pc could match the
// root component rc. "com.apple.*" stands for any name with that prefix: pc
// could match one if its literal prefix (before any glob character) and
// "com.apple." agree as far as both go.
func componentCouldMatch(pc, rc string) bool {
	if prefix, ok := strings.CutSuffix(rc, "*"); ok {
		lit := pc
		if i := strings.IndexAny(pc, `*?[\`); i >= 0 {
			lit = pc[:i]
		}
		return strings.HasPrefix(prefix, lit) || strings.HasPrefix(lit, prefix)
	}
	ok, err := path.Match(pc, rc)
	return ok || err != nil
}

// lintArgv checks the shape of a native argv: a known tool, at most one
// whole-element "{id}", and no interpolation or shell characters. The exact
// allow-lists checked in Validate are the real gate; this is defense in depth.
func lintArgv(argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	if !slices.Contains(NativeTools, argv[0]) {
		return fmt.Errorf("program %q is not an allowed native tool", argv[0])
	}
	placeholders := 0
	for _, a := range argv {
		if a == Placeholder {
			placeholders++
		} else if strings.Contains(a, "{") || strings.ContainsAny(a, "$`;|&<>\n") {
			return fmt.Errorf("argument %q looks like interpolation or shell syntax", a)
		}
	}
	if placeholders > 1 {
		return errors.New("at most one {id} placeholder")
	}
	return nil
}

// preconditionArgRE is what the argument of a "name:" precondition may look
// like. Phase 5 passes it to a process check, so it may not start with "-"
// or contain path separators, control or shell characters.
var preconditionArgRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]*$`)

func validPrecondition(pc string) bool {
	for _, p := range Preconditions {
		if strings.HasSuffix(p, ":") {
			if arg, ok := strings.CutPrefix(pc, p); ok && preconditionArgRE.MatchString(arg) {
				return true
			}
		} else if pc == p {
			return true
		}
	}
	return false
}

// paramInt reads an integer parameter with a default.
func paramInt(r Recipe, key string, def int64) (int64, error) {
	v, ok := r.Params[key]
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("recipe %s: params.%s: %w", r.ID, key, err)
	}
	return n, nil
}

func unixTime(sec int64) time.Time { return time.Unix(sec, 0).UTC() }
