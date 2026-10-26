package recipes

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"macdoc/internal/model"
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

// forbiddenArgs may never appear in a native argv (docs/SAFETY_MODEL.md §5).
var forbiddenArgs = []string{"--volumes", "sudo", "sh", "bash", "zsh", "rm", "-rf", "--force-all"}

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

func lintArgv(argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	if !slices.Contains(NativeTools, argv[0]) {
		return fmt.Errorf("program %q is not an allowed native tool", argv[0])
	}
	placeholders := 0
	for i, a := range argv {
		if slices.Contains(forbiddenArgs, a) || strings.HasPrefix(a, "--volumes") {
			return fmt.Errorf("forbidden argument %q", a)
		}
		if i > 0 && a == "prune" && argv[i-1] == "system" {
			return errors.New(`"system prune" is forbidden`)
		}
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

func validPrecondition(pc string) bool {
	for _, p := range Preconditions {
		if strings.HasSuffix(p, ":") {
			if arg, ok := strings.CutPrefix(pc, p); ok && arg != "" && !strings.ContainsAny(arg, "/\n") {
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
