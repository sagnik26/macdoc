// Package recipes defines what macdoc may ever act on. A recipe describes one
// kind of reclaimable space (where it lives, how risky it is, how it would be
// reclaimed, and what it costs to get back); a detector finds concrete
// instances of it. Recipes are data (embedded YAML in catalog/); detectors are
// small read-only Go functions registered by name.
//
// Every action macdoc can take comes from a recipe. An item that no recipe
// covers is advice-only (docs/adr/0002-ids-not-paths.md).
package recipes

import (
	"context"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/projects"
)

// Recipe is one entry of the catalog (docs/RECIPE_CATALOG.md §1).
type Recipe struct {
	ID        string       `yaml:"id"`        // dotted, e.g. "xcode.derived_data"
	Title     string       `yaml:"title"`     // short human label
	Category  string       `yaml:"category"`  // one of Categories
	Toolchain string       `yaml:"toolchain"` // one of Toolchains
	Risk      model.Risk   `yaml:"risk"`      // base risk; context may raise it, never lower
	Method    model.Method `yaml:"method"`    // native | quarantine | advise

	Detector string            `yaml:"detector"` // registered detector name
	Params   map[string]string `yaml:"params"`   // detector parameters ("~" expands to home)

	// Allow lists the path patterns a quarantine target must match
	// (safety.CheckTarget semantics). Required for method quarantine; must be
	// empty otherwise.
	Allow []string `yaml:"allow"`

	// Native is required for method native and forbidden otherwise.
	Native *Native `yaml:"native"`

	// KeepChildrenOf and KeepNames tell the scanner which small directories
	// to keep so detectors can see them: every direct child of these
	// "~"-relative directories, and every directory with one of these base
	// names (e.g. "node_modules").
	KeepChildrenOf []string `yaml:"keep_children_of"`
	KeepNames      []string `yaml:"keep_names"`

	StaleDays     int           `yaml:"stale_days"`    // 0: no idle requirement
	Preconditions []string      `yaml:"preconditions"` // see Preconditions
	Restore       model.Restore `yaml:"restore"`
	Explain       string        `yaml:"explain"`          // what it is, why it exists, what removing it means
	Confidence    string        `yaml:"confidence_notes"` // e.g. "may include APFS clones"
	// Steps are what the user can do by hand, shown in the explanation.
	// Advice-only recipes use them for the cleanup macdoc won't do itself;
	// text in `backticks` is a command to copy.
	Steps []string `yaml:"steps"`
}

// Native describes the owning tool's command. Argv elements are fixed
// strings; the single placeholder "{id}" may appear as a whole element and is
// replaced by Finding.NativeArg, which a detector must have taken verbatim
// from the tool's own listing.
type Native struct {
	Argv       []string `yaml:"argv"`
	DryRunArgv []string `yaml:"dry_run_argv"` // the tool's own dry-run, if it has one
}

// Placeholder is the only substitution allowed in Native.Argv.
const Placeholder = "{id}"

// Categories are the allowed Recipe.Category values.
var Categories = []string{
	"build-artifacts", "package-caches", "simulators-emulators", "containers",
	"ai-models", "app-caches", "logs", "downloads", "backups", "system",
}

// Toolchains are the allowed Recipe.Toolchain values.
var Toolchains = []string{
	"xcode", "cocoapods", "android", "node", "react-native", "homebrew",
	"docker", "python", "ai", "system",
}

// Preconditions are the allowed precondition names. A precondition is either
// a bare name or "name:arg" for names ending in ":" here (e.g.
// "app_not_running:Xcode"). They are checked at plan/apply time (Phase 5/6).
var Preconditions = []string{
	"app_not_running:", "docker_reachable", "emulator_not_running",
	"simulator_not_running", "gradle_daemons_stopped", "metro_not_running",
	"ollama_model_not_loaded", "git_clean",
}

// NativeTools are the only programs a native argv may start with. They are
// the tools of the exact argv allow-list in load.go.
var NativeTools = []string{
	"brew", "xcrun", "docker", "npm", "pnpm", "yarn", "pip3", "uv", "conda",
	"ollama", "avdmanager", "pod",
}

// Finding is one concrete instance a detector found for a recipe.
type Finding struct {
	// Target is the absolute path the opportunity is about. For quarantine
	// recipes it is what would be moved and must pass safety.CheckTarget
	// with the recipe's Allow patterns; for native and advise recipes it is
	// informational (may be "").
	Target string
	// NativeArg replaces "{id}" in Native.Argv. It must come verbatim from
	// the owning tool's listing (e.g. a simulator runtime identifier).
	NativeArg string
	Title     string      // e.g. "iOS 17.2 simulator runtime"; "" = recipe title
	Bytes     model.Bytes // reclaimable size and its source
	LastUsed  time.Time   // zero if unknown
	Note      string      // optional extra explanation for this instance

	// MinRisk raises the recipe's risk to at least this tier for this
	// instance (never lowers it), with RiskReason saying why, e.g. the
	// project has uncommitted changes.
	MinRisk    model.Risk
	RiskReason string
}

// NodeSource gives detectors read-only access to the latest stored scan.
type NodeSource interface {
	// Nodes returns kept nodes equal to or below prefix (absolute path),
	// at most maxDepth levels below it (0 = unlimited), Bytes descending.
	Nodes(prefix string, maxDepth int) ([]model.Node, error)
}

// Env is what a detector may use. Detectors must be read-only: no writes,
// no deletes, external commands only through Runner.
type Env struct {
	Home   string          // canonical home directory
	Nodes  NodeSource      // latest scan (may be nil if there is none)
	Runner platform.Runner // read-only commands (e.g. `xcrun simctl list -j`)
	Now    time.Time
	// Projects returns the projects found in the latest scan (computed once
	// and shared by all detectors). Nil if unavailable.
	Projects func() (projects.Result, error)
	// Cache, when set, lets the slow Homebrew dry runs reuse a recent
	// result (RunCachedDryRun). Display only: leave it nil anywhere that
	// decides or acts (executor, planner.Verify, listers).
	Cache Cache
}

// Detector finds instances of a recipe. A detector that can't run (tool not
// installed, no scan) returns nil findings and a nil error; real failures
// return an error, which callers show as a warning.
type Detector func(ctx context.Context, env Env, r Recipe) ([]Finding, error)
