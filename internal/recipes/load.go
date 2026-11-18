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

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/safety"
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
	for i := range all {
		if err := deriveToolchain(&all[i]); err != nil {
			errs = append(errs, err)
		}
	}
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
	{"qlmanage", "-r", "cache"},
	{"qlmanage", "-r"},
	{"launchctl", "remove", Placeholder},
}

// PrivilegedArgvAllowed is the exact allow-list of commands that may run with
// administrator rights (ADR 0009). Each is a maintenance command that only
// refreshes a cache and takes no path or argument from outside. A recipe
// marked privileged must use exactly one of these, and no other recipe may.
var PrivilegedArgvAllowed = [][]string{
	{"dscacheutil", "-flushcache"},
	{"killall", "-HUP", "mDNSResponder"},
	{"purge"},
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

// problems collects the lint messages for one recipe, in the order found.
type problems struct {
	id   string
	errs []error
}

// add records one problem, prefixed with the recipe ID.
func (p *problems) add(format string, a ...any) {
	p.errs = append(p.errs, fmt.Errorf("recipe %s: "+format, append([]any{p.id}, a...)...))
}

// Validate checks one recipe, including the safety lint from the P0-R1/P0-W3
// carry-overs. It returns all problems joined. The checks run in a fixed
// order, which is also the order of the messages.
func Validate(r Recipe) error {
	p := &problems{id: r.ID}
	identity(p, r)
	vocabulary(p, r)
	method(p, r)
	allows(p, r)
	children(p, r)
	names(p, r)
	keepFiles(p, r)
	argvs(p, r)
	conditions(p, r)
	return errors.Join(p.errs...)
}

// identity checks the ID, the texts and the manual steps.
func identity(p *problems, r Recipe) {
	if r.ID == "" || strings.ContainsAny(r.ID, " /") || !strings.Contains(r.ID, ".") {
		p.add("id must be dotted with no spaces or slashes")
	}
	if r.Title == "" || r.Explain == "" {
		p.add("title and explain are required")
	}
	for _, s := range r.Steps {
		if strings.TrimSpace(s) == "" || strings.Count(s, "`")%2 != 0 {
			p.add("steps must be non-empty with paired backticks")
		}
	}
}

// vocabulary checks every field that must come from a fixed set.
func vocabulary(p *problems, r Recipe) {
	if !slices.Contains(Categories, r.Category) {
		p.add("unknown category %q", r.Category)
	}
	if !slices.Contains(Toolchains, r.Toolchain) {
		p.add("unknown toolchain %q", r.Toolchain)
	}
	risk(p, r)
	if _, ok := Lookup(r.Detector); !ok {
		p.add("unknown detector %q", r.Detector)
	}
	restore(p, r)
	if r.StaleDays < 0 {
		p.add("stale_days must be >= 0")
	}
	if v, ok := r.Params["dirty_risk"]; ok && !slices.Contains([]model.Risk{model.RiskCaution, model.RiskRisky}, model.Risk(v)) {
		p.add("params.dirty_risk must be CAUTION or RISKY")
	}
	matchProblems(p, r)
}

func risk(p *problems, r Recipe) {
	if r.Regenerable && r.Risk != model.RiskSafe {
		p.add("regenerable is only for SAFE recipes")
	}
	switch r.Risk {
	case model.RiskSafe, model.RiskCaution, model.RiskRisky, model.RiskAdvise:
	default:
		p.add("risk must be SAFE, CAUTION, RISKY or ADVISE (BLOCKED things are not recipes)")
	}
}

func restore(p *problems, r Recipe) {
	switch r.Restore.Kind {
	case model.RestoreRebuild, model.RestoreRedownload, model.RestoreIrreplaceable, model.RestoreNone:
	default:
		p.add("restore.kind must be rebuild, redownload, irreplaceable or none")
	}
}

// method checks that the recipe's method agrees with its allow patterns,
// native command and risk.
func method(p *problems, r Recipe) {
	switch r.Method {
	case model.MethodQuarantine:
		quarantine(p, r)
	case model.MethodNative:
		native(p, r)
	case model.MethodAdvise:
		advise(p, r)
	default:
		p.add("method must be native, quarantine or advise")
	}
	if r.Risk == model.RiskAdvise && r.Method != model.MethodAdvise {
		p.add("risk ADVISE requires method advise")
	}
}

func quarantine(p *problems, r Recipe) {
	if len(r.Allow) == 0 {
		p.add("quarantine recipes need allow patterns")
	}
	if r.Native != nil {
		p.add("quarantine recipes must not have native")
	}
	if r.Restore.Kind == model.RestoreIrreplaceable && !quarantinedTrash(r) {
		p.add("irreplaceable data must be advise-only")
	}
}

// trashFolder is the Trash, the one folder keep_children_of may name only one
// level below home, and only for a recipe that passes quarantinedTrash.
const trashFolder = "~/.Trash"

// quarantinedTrash is the one carve-out from "irreplaceable data is advice
// only": items in the Trash, which a person already chose to throw away. They
// are moved into macdoc's quarantine first, so they stay restorable until it
// expires. The recipe must be CAUTION, say how to get them back, and allow
// nothing outside ~/.Trash/.
func quarantinedTrash(r Recipe) bool {
	if r.Risk != model.RiskCaution || strings.TrimSpace(r.Restore.Note) == "" || len(r.Allow) == 0 {
		return false
	}
	for _, pat := range r.Allow {
		if !strings.HasPrefix(pat, "~/.Trash/") {
			return false
		}
	}
	return true
}

func native(p *problems, r Recipe) {
	if r.Native == nil || len(r.Native.Argv) == 0 {
		p.add("native recipes need native.argv")
	}
	if len(r.Allow) != 0 {
		p.add("native recipes must not have allow patterns")
	}
}

func advise(p *problems, r Recipe) {
	if r.Native != nil || len(r.Allow) != 0 {
		p.add("advise recipes must not have native or allow")
	}
	if r.Risk != model.RiskAdvise {
		p.add("advise recipes must have risk ADVISE")
	}
}

// allows lints every allow pattern: its shape, then what it could match.
func allows(p *problems, r Recipe) {
	for _, pat := range r.Allow {
		if pat == safety.AppsPattern {
			appsAllow(p, r)
			continue
		}
		if pat == supportPattern && r.Method == model.MethodQuarantine && r.Risk == model.RiskRisky && r.Detector == "app_leftovers" {
			continue // the one exception to the protected-overlap rule (ADR 0011)
		}
		err := lintPattern(pat)
		if err == nil && pat == containerCachePattern && r.Method == model.MethodQuarantine && r.Risk == model.RiskCaution && r.Detector == "container_caches" {
			continue // the second exception to the protected-overlap rule: see containerCachePattern
		}
		if err == nil {
			err = LintAllowProtected(pat)
		}
		if err != nil {
			p.add("allow %q: %v", pat, err)
		}
	}
}

// appsAllow lets the one /Applications pattern through, but only for a RISKY
// quarantine recipe, so a person confirms each app on its own.
func appsAllow(p *problems, r Recipe) {
	if r.Method != model.MethodQuarantine || r.Risk != model.RiskRisky {
		p.add("allow %q is only for a RISKY quarantine recipe", safety.AppsPattern)
	}
}

func children(p *problems, r Recipe) {
	for _, pat := range r.KeepChildrenOf {
		if pat == trashFolder && r.Method == model.MethodQuarantine && quarantinedTrash(r) {
			continue // the one 1-deep folder: so small items in the Trash are seen
		}
		if err := lintPattern(pat); err != nil || strings.Contains(pat, "*") {
			p.add("keep_children_of %q must be a literal ~-relative directory at least 2 deep", pat)
		}
	}
}

func names(p *problems, r Recipe) {
	for _, n := range r.KeepNames {
		if n == "" || strings.ContainsAny(n, "/*?[") || n == "." || n == ".." {
			p.add("keep_names %q must be a plain directory name", n)
		}
	}
}

// keepFiles checks each keep_files entry is a literal ~-relative folder
// followed by /*.ext.
func keepFiles(p *problems, r Recipe) {
	for _, pat := range r.KeepFiles {
		if _, _, ok := SplitKeepFile(pat); !ok {
			p.add("keep_files %q must be a literal ~-relative folder followed by /*.ext, like ~/Downloads/*.dmg", pat)
		}
	}
}

// argvs lints the native argv and dry-run argv, then pins both to their
// exact allow-lists.
func argvs(p *problems, r Recipe) {
	if r.Native == nil {
		return
	}
	if r.Privileged {
		privilegedArgv(p, r)
		return
	}
	for _, argv := range [][]string{r.Native.Argv, r.Native.DryRunArgv} {
		if err := lintArgv(argv); err != nil {
			p.add("native argv %q: %v", argv, err)
		}
	}
	if len(r.Native.Argv) > 0 && !ArgvIn(r.Native.Argv, NativeArgvAllowed) {
		p.add("native argv %q: native argv not in the allow-list", r.Native.Argv)
	}
	if len(r.Native.DryRunArgv) > 0 && !ArgvIn(r.Native.DryRunArgv, DryRunArgvAllowed) {
		p.add("native dry_run_argv %q: dry-run argv not in the allow-list", r.Native.DryRunArgv)
	}
}

// privilegedArgv pins a privileged recipe to the exact allow-list: no
// placeholder, no dry-run argv, nothing but a listed command.
func privilegedArgv(p *problems, r Recipe) {
	if r.Method != model.MethodNative {
		p.add("a privileged recipe must use method native")
	}
	if !ArgvIn(r.Native.Argv, PrivilegedArgvAllowed) {
		p.add("privileged argv %q is not in the privileged allow-list", r.Native.Argv)
	}
	if len(r.Native.DryRunArgv) > 0 {
		p.add("a privileged recipe must not have a dry_run_argv")
	}
	if r.Risk == model.RiskSafe {
		p.add("a privileged recipe must be CAUTION or higher")
	}
}

func conditions(p *problems, r Recipe) {
	for _, pc := range r.Preconditions {
		if !validPrecondition(pc) {
			p.add("unknown precondition %q", pc)
		}
	}
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

// supportPattern is the children of ~/Library/Application Support. Two
// protected folders live directly below it (macdoc's own, and MobileSync
// backups), so the usual overlap rule would reject it; CheckTarget refuses
// both of them at plan and apply time, and the leftovers detector only offers
// bundle-identifier names, which neither is (ADR 0011).
const supportPattern = "~/Library/Application Support/*"

// containerCachePattern is the Caches folder of every sandboxed app's
// container. The bundle-ID wildcard could in theory match Apple's protected
// containers (com.apple.*), which the lint cannot rule out, so this one pattern
// is exempt from the overlap rule, and only for a CAUTION quarantine recipe
// that uses the container_caches detector. That detector never offers a
// com.apple.* container, and CheckTarget refuses Apple's containers again at
// plan and apply time.
const containerCachePattern = "~/Library/Containers/*/Data/Library/Caches"

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
