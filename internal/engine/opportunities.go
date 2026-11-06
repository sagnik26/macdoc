package engine

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"

	"github.com/sagnik26/macdoc/internal/config"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/projects"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
	"github.com/sagnik26/macdoc/internal/store"
)

// OpportunityOptions configures Opportunities. Zero values use the real
// store, catalog and clock.
type OpportunityOptions struct {
	Home      string
	StorePath string // default store.DefaultPath(Home)
	Runner    platform.Runner
	Now       time.Time        // default time.Now()
	Catalog   *recipes.Catalog // default recipes.Load()
	// MinBytes hides items of known size below it (0: 1 MB; negative: show
	// everything).
	MinBytes int64
}

// OpportunityResult is the data behind `macdoc wins`, `dev` and `explain`.
type OpportunityResult struct {
	Scan          model.ScanInfo       `json:"scan"`
	ScanAge       string               `json:"scan_age"`      // e.g. "2h ago"
	Opportunities []model.Opportunity  `json:"opportunities"` // ranked
	Totals        map[model.Risk]int64 `json:"totals"`        // known bytes per risk tier
	Warnings      []string             `json:"warnings,omitempty"`
}

// ErrNoScan is returned when there is no complete scan to work from.
var ErrNoScan = errors.New(`no scan yet. Run "macdoc doctor" (it scans automatically) or "macdoc doctor scan"`)

// Tunables; variables so tests can shorten them.
var (
	DetectorTimeout     = 90 * time.Second
	detectorConcurrency = 4
)

const (
	staleScanAge = 6 * time.Hour
	recentChange = 7 // days: a SAFE item changed more recently becomes CAUTION
	// minShownBytes is the default MinBytes: smaller items aren't worth a
	// decision. Items of unknown size are always shown.
	minShownBytes = 1_000_000
	idLen         = 6
	idLenExtended = 8
)

// TierOrder is the display and ranking order of risk tiers.
var TierOrder = []model.Risk{model.RiskSafe, model.RiskCaution, model.RiskRisky, model.RiskAdvise}

// Opportunities runs every recipe's detector against the latest complete
// scan and turns the findings into ranked, safety-checked opportunities. It
// is read-only and never starts a scan.
func Opportunities(ctx context.Context, o OpportunityOptions) (OpportunityResult, error) {
	if o.Home == "" {
		return OpportunityResult{}, errors.New("opportunities: home directory is required")
	}
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.MinBytes == 0 {
		o.MinBytes = minShownBytes
	}
	if o.Catalog == nil {
		cat, err := recipes.Load()
		if err != nil {
			return OpportunityResult{}, err
		}
		o.Catalog = cat
	}
	policy, _, err := config.Policy(o.Home)
	if err != nil {
		return OpportunityResult{}, err
	}

	st, err := store.Open(o.StorePath)
	if err != nil {
		return OpportunityResult{}, err
	}
	defer func() { _ = st.Close() }()
	info, err := st.LatestScan(model.ScanComplete)
	if errors.Is(err, store.ErrNoScan) {
		return OpportunityResult{}, ErrNoScan
	}
	if err != nil {
		return OpportunityResult{}, err
	}

	res := OpportunityResult{Scan: info, Totals: map[model.Risk]int64{}}
	age := o.Now.Sub(scanTime(info))
	res.ScanAge = FormatAge(age)
	if age > staleScanAge {
		res.Warnings = append(res.Warnings, fmt.Sprintf(`results are from a scan %d hours old; run "macdoc doctor scan" to refresh`, int(age.Hours())))
	}

	nodes := ScanNodes{St: st, ScanID: info.ID}
	env := recipes.Env{Home: o.Home, Nodes: nodes, Runner: o.Runner, Now: o.Now}
	env.Projects = sync.OnceValues(func() (projects.Result, error) {
		return discoverProjects(ctx, o, nodes, info.ID)
	})
	runs := runDetectors(ctx, env, o.Catalog.Recipes)

	var ops []model.Opportunity
	unknownIdleCount, tiny := 0, 0
	for _, run := range runs {
		if run.err != "" {
			res.Warnings = append(res.Warnings, run.recipe.ID+": "+run.err)
			continue
		}
		for _, f := range run.findings {
			op, warn, ok := Classify(run.recipe, f, info.ID, o.Now, policy, len(run.findings))
			switch warn {
			case "":
			case unknownIdle:
				unknownIdleCount++
			default:
				res.Warnings = append(res.Warnings, warn)
			}
			switch {
			case !ok:
			case op.Bytes.Source != model.SourceUnknown && op.Bytes.Value < o.MinBytes:
				tiny++
			default:
				ops = append(ops, op)
			}
		}
	}
	if tiny > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d item(s) under 1 MB not shown", tiny))
	}
	if unknownIdleCount > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d item(s) skipped because their last use is unknown", unknownIdleCount))
	}

	ops, dupWarnings := DropEqualTargets(dedupe(ops))
	res.Warnings = append(res.Warnings, dupWarnings...)
	ops, merged := mergeOverlaps(ops)
	if merged > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d item(s) inside another listed folder were merged into it", merged))
	}
	subtractContained(ops)
	AssignIDs(ops)
	rank(ops)
	for _, op := range ops {
		if op.Bytes.Source != model.SourceUnknown {
			res.Totals[op.Risk] += op.Bytes.Value
		}
	}
	if ops == nil {
		ops = []model.Opportunity{}
	}
	res.Opportunities = ops
	return res, nil
}

// FindOpportunity returns the opportunity with the given ID ("O-" prefix
// optional, case-insensitive).
func FindOpportunity(res OpportunityResult, id string) (model.Opportunity, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.TrimPrefix(id, "o-")
	for _, op := range res.Opportunities {
		if strings.TrimPrefix(op.ID, "O-") == id {
			return op, true
		}
	}
	return model.Opportunity{}, false
}

// ToolchainTotal groups opportunities of one toolchain.
type ToolchainTotal struct {
	Toolchain     string              `json:"toolchain"`
	Bytes         int64               `json:"bytes"` // known bytes only
	Opportunities []model.Opportunity `json:"opportunities"`
}

// ByToolchain groups ranked opportunities by toolchain, biggest known total
// first. Order within a group is kept.
func ByToolchain(ops []model.Opportunity) []ToolchainTotal {
	idx := map[string]int{}
	var out []ToolchainTotal
	for _, op := range ops {
		i, ok := idx[op.Toolchain]
		if !ok {
			i = len(out)
			idx[op.Toolchain] = i
			out = append(out, ToolchainTotal{Toolchain: op.Toolchain})
		}
		out[i].Opportunities = append(out[i].Opportunities, op)
		if op.Bytes.Source != model.SourceUnknown {
			out[i].Bytes += op.Bytes.Value
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Toolchain < out[j].Toolchain
	})
	return out
}

// FormatAge renders a duration as "just now", "5m ago", "2h ago" or "3d ago".
func FormatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func scanTime(info model.ScanInfo) time.Time {
	if !info.FinishedAt.IsZero() {
		return info.FinishedAt
	}
	return info.StartedAt
}

// detectorRun is one recipe's detector outcome.
type detectorRun struct {
	recipe   recipes.Recipe
	findings []recipes.Finding
	err      string
}

// runDetectors runs every recipe's detector with bounded concurrency, a
// per-detector timeout and panic recovery. Results keep catalog order.
func runDetectors(ctx context.Context, env recipes.Env, rs []recipes.Recipe) []detectorRun {
	out := make([]detectorRun, len(rs))
	sem := make(chan struct{}, detectorConcurrency)
	var wg sync.WaitGroup
	for i, r := range rs {
		out[i].recipe = r
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i].findings, out[i].err = runDetector(ctx, env, r)
		}()
	}
	wg.Wait()
	return out
}

// runDetector runs one detector. A detector that ignores its context is
// abandoned at the timeout (its goroutine finishes on its own; the result is
// discarded).
func runDetector(ctx context.Context, env recipes.Env, r recipes.Recipe) ([]recipes.Finding, string) {
	d, ok := recipes.Lookup(r.Detector)
	if !ok {
		return nil, fmt.Sprintf("unknown detector %q", r.Detector)
	}
	ctx, cancel := context.WithTimeout(ctx, DetectorTimeout)
	defer cancel()
	type result struct {
		fs  []recipes.Finding
		err string
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- result{err: fmt.Sprintf("detector panicked: %v", p)}
			}
		}()
		fs, err := d(ctx, env, r)
		if err != nil {
			done <- result{err: err.Error()}
			return
		}
		done <- result{fs: fs}
	}()
	select {
	case res := <-done:
		return res.fs, res.err
	case <-ctx.Done():
		return nil, fmt.Sprintf("detector stopped after %s: %v", DetectorTimeout, ctx.Err())
	}
}

// unknownIdle is the warning Classify returns for a finding dropped because
// its recipe needs a minimum idle time and the last use is unknown. The
// caller counts these and reports them once.
const unknownIdle = "\x00unknown-idle"

// Classify turns one finding into an opportunity, or drops it. A dropped
// finding may come with a warning (or unknownIdle).
func Classify(r recipes.Recipe, f recipes.Finding, scanID string, now time.Time, policy *safety.Policy, siblings int) (model.Opportunity, string, bool) {
	op := model.Opportunity{
		ScanID:    scanID,
		RecipeID:  r.ID,
		Title:     f.Title,
		Target:    f.Target,
		Bytes:     f.Bytes,
		Risk:      r.Risk,
		Method:    r.Method,
		IdleDays:  -1,
		Restore:   r.Restore,
		Explain:   r.Explain,
		Steps:     r.Steps,
		Category:  r.Category,
		Toolchain: r.Toolchain,
		Note:      f.Note,
	}
	if op.Bytes.Source == "" {
		op.Bytes.Source = model.SourceUnknown
	}

	switch r.Method {
	case model.MethodNative:
		if r.Native == nil || len(r.Native.Argv) == 0 || !recipes.ToolInstalled(r.Native.Argv[0]) {
			return op, "", false
		}
		cmd, err := Substitute(r.Native.Argv, f.NativeArg)
		if err != nil {
			return op, fmt.Sprintf("%s: skipped an item: %v", r.ID, err), false
		}
		dry, err := Substitute(r.Native.DryRunArgv, f.NativeArg)
		if err != nil {
			return op, fmt.Sprintf("%s: skipped an item: %v", r.ID, err), false
		}
		op.Command, op.DryRun = cmd, dry
	case model.MethodQuarantine:
		if err := policy.CheckTarget(f.Target, r.Allow); err != nil {
			return op, fmt.Sprintf("%s: skipped %s: %v", r.ID, f.Target, err), false
		}
	}

	if !f.LastUsed.IsZero() {
		op.IdleDays = max(0, int(now.Sub(f.LastUsed).Hours()/24))
	}
	if r.StaleDays > 0 {
		switch {
		case op.IdleDays < 0:
			// Unknown idle time can't prove the item is stale (P3-R1).
			return op, unknownIdle, false
		case op.IdleDays < r.StaleDays:
			return op, "", false
		}
	}
	if op.Risk == model.RiskSafe && op.IdleDays >= 0 && op.IdleDays < recentChange {
		op.Risk, op.RiskReason = model.RiskCaution, "it changed in the last 7 days"
	}
	if f.MinRisk != "" && tierIndex(f.MinRisk) > tierIndex(op.Risk) && f.MinRisk != model.RiskAdvise {
		op.Risk, op.RiskReason = f.MinRisk, f.RiskReason
	}
	op.Confidence = sourceConfidence(op.Bytes.Source)

	if op.Title == "" {
		op.Title = r.Title
		if MultiInstance(r, siblings) && f.Target != "" {
			op.Title += " · " + instanceName(r, f.Target)
		}
	}
	op.ID = OpportunityID(scanID, r.ID, f.Target, f.NativeArg)
	return op, "", true
}

// RaisedFrom reports the recipe's base risk if op's risk was raised above it.
func RaisedFrom(op model.Opportunity, cat *recipes.Catalog) (model.Risk, bool) {
	if cat == nil {
		return "", false
	}
	r, ok := cat.Get(op.RecipeID)
	if !ok || r.Risk == op.Risk {
		return "", false
	}
	return r.Risk, true
}

// MultiInstance reports whether a recipe yields several findings (or is the
// one-per-folder "children" or "derived_data" detector), so each needs its own name in the
// title.
func MultiInstance(r recipes.Recipe, siblings int) bool {
	return r.Detector == "children" || r.Detector == "derived_data" || siblings > 1
}

// instanceName is the base name of target, or of its parent when the base
// name is a fixed folder name the recipe looks for (e.g. "node_modules"),
// which would say nothing about which instance it is.
func instanceName(r recipes.Recipe, target string) string {
	base := filepath.Base(target)
	if slices.Contains(r.KeepNames, base) {
		return filepath.Base(filepath.Dir(target))
	}
	return base
}

// Substitute and CheckNativeArg live in recipes so the planner can re-check
// sealed argv against the same rules.
var (
	Substitute     = recipes.Substitute
	CheckNativeArg = recipes.CheckNativeArg
)

func sourceConfidence(s model.Source) float64 {
	switch s {
	case model.SourceMeasured:
		return 1.0
	case model.SourceTool:
		return 0.9
	case model.SourceEstimated:
		return 0.6
	}
	return 0.3
}

func actionable(op model.Opportunity) bool {
	return op.Target != "" && (op.Method == model.MethodNative || op.Method == model.MethodQuarantine)
}

func foldPath(p string) string { return strings.ToLower(norm.NFC.String(filepath.Clean(p))) }

// mergeOverlaps drops actionable opportunities whose target is strictly
// below another actionable opportunity's target. Advise items never absorb
// others and are never absorbed.
func mergeOverlaps(ops []model.Opportunity) ([]model.Opportunity, int) {
	targets := map[string]bool{}
	for _, op := range ops {
		if actionable(op) {
			targets[foldPath(op.Target)] = true
		}
	}
	var out []model.Opportunity
	merged := 0
	for _, op := range ops {
		if actionable(op) && HasAncestor(targets, foldPath(op.Target)) {
			merged++
			continue
		}
		out = append(out, op)
	}
	return out, merged
}

// DropEqualTargets keeps one actionable opportunity per target when
// different recipes report the same path: the higher risk (more
// conservative), then the lower recipe ID. Findings of the same recipe are
// left alone (they differ by tool identifier; exact duplicates are already
// gone).
func DropEqualTargets(ops []model.Opportunity) ([]model.Opportunity, []string) {
	winner := map[string]int{} // folded target -> index in ops
	for i, op := range ops {
		if !actionable(op) {
			continue
		}
		k := foldPath(op.Target)
		j, ok := winner[k]
		if !ok || better(op, ops[j]) {
			winner[k] = i
		}
	}
	var out []model.Opportunity
	var warnings []string
	for i, op := range ops {
		if actionable(op) {
			w := ops[winner[foldPath(op.Target)]]
			if winner[foldPath(op.Target)] != i && w.RecipeID != op.RecipeID {
				warnings = append(warnings, fmt.Sprintf("%s duplicates %s for %s", op.RecipeID, w.RecipeID, op.Target))
				continue
			}
		}
		out = append(out, op)
	}
	return out, warnings
}

// better reports whether a should be kept over b for the same target.
func better(a, b model.Opportunity) bool {
	if ta, tb := tierIndex(a.Risk), tierIndex(b.Risk); ta != tb {
		return ta > tb
	}
	return a.RecipeID < b.RecipeID
}

// containment pairs: the container recipe's size includes the contained
// recipe's items, which are also listed on their own.
var containment = []struct {
	container, contained, note string
}{
	{"docker.unused_images", "docker.dangling_images", "excludes dangling images listed separately"},
}

// sourceStrength orders sources from weakest to strongest.
var sourceStrength = []model.Source{model.SourceUnknown, model.SourceEstimated, model.SourceTool, model.SourceMeasured}

// subtractContained removes the contained recipe's bytes from the container
// recipe's size, so totals don't count them twice. It only acts when both
// sizes are known.
func subtractContained(ops []model.Opportunity) {
	for _, c := range containment {
		var sum int64
		weakest := model.SourceMeasured
		found := false
		for _, op := range ops {
			if op.RecipeID != c.contained {
				continue
			}
			if op.Bytes.Source == model.SourceUnknown {
				found = false
				break
			}
			found = true
			sum += op.Bytes.Value
			weakest = Weaker(weakest, op.Bytes.Source)
		}
		if !found {
			continue
		}
		for i := range ops {
			op := &ops[i]
			if op.RecipeID != c.container || op.Bytes.Source == model.SourceUnknown {
				continue
			}
			op.Bytes.Value = max(0, op.Bytes.Value-sum)
			op.Bytes.Source = Weaker(op.Bytes.Source, weakest)
			op.Confidence = sourceConfidence(op.Bytes.Source)
			if op.Note != "" {
				op.Note += "; "
			}
			op.Note += c.note
		}
	}
}

// Weaker returns the less trustworthy of two number sources, so a total
// built from mixed sources never claims more certainty than its weakest part.
func Weaker(a, b model.Source) model.Source {
	if slices.Index(sourceStrength, a) < slices.Index(sourceStrength, b) {
		return a
	}
	return b
}

// HasAncestor reports whether any strict ancestor directory of p is in set.
func HasAncestor(set map[string]bool, p string) bool {
	if p == "/" {
		return false
	}
	for d := filepath.Dir(p); ; d = filepath.Dir(d) {
		if set[d] {
			return true
		}
		if d == "/" || d == "." {
			return false
		}
	}
}

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// OpportunityID returns the full-length lowercase ID body (without "O-").
func OpportunityID(scanID, recipeID, target, nativeArg string) string {
	sum := sha256.Sum256([]byte(scanID + "\x00" + recipeID + "\x00" + target + "\x00" + nativeArg))
	return strings.ToLower(idEncoding.EncodeToString(sum[:]))
}

// AssignIDs shortens each full ID to 6 characters, extending colliding IDs
// (all members of the collision) to 8, then further if still needed.
func AssignIDs(ops []model.Opportunity) {
	full := make([]string, len(ops))
	n := make([]int, len(ops))
	for i, op := range ops {
		full[i] = op.ID
		n[i] = idLen
	}
	step := idLenExtended - idLen
	for length := idLen; ; length += step {
		groups := map[string][]int{}
		for i := range ops {
			if n[i] == length {
				groups[full[i][:length]] = append(groups[full[i][:length]], i)
			}
		}
		again := false
		for _, g := range groups {
			// Identical full IDs can't be told apart; Opportunities removes
			// such duplicates before this runs.
			if len(g) < 2 || length+step > len(full[g[0]]) || allSame(full, g) {
				continue
			}
			for _, i := range g {
				n[i] = length + step
			}
			again = true
		}
		if !again {
			break
		}
	}
	for i := range ops {
		ops[i].ID = "O-" + full[i][:n[i]]
	}
}

func allSame(full []string, g []int) bool {
	for _, i := range g[1:] {
		if full[i] != full[g[0]] {
			return false
		}
	}
	return true
}

// dedupe drops repeated findings (same recipe, target and tool identifier,
// hence the same full ID), keeping the first.
func dedupe(ops []model.Opportunity) []model.Opportunity {
	seen := map[string]bool{}
	out := ops[:0]
	for _, op := range ops {
		if seen[op.ID] {
			continue
		}
		seen[op.ID] = true
		out = append(out, op)
	}
	return out
}

func tierIndex(r model.Risk) int {
	if i := slices.Index(TierOrder, r); i >= 0 {
		return i
	}
	return len(TierOrder)
}

// rank sorts by tier, then expected bytes (value × confidence) descending,
// then ID.
func rank(ops []model.Opportunity) {
	sort.SliceStable(ops, func(i, j int) bool {
		a, b := ops[i], ops[j]
		if ta, tb := tierIndex(a.Risk), tierIndex(b.Risk); ta != tb {
			return ta < tb
		}
		if wa, wb := float64(a.Bytes.Value)*a.Confidence, float64(b.Bytes.Value)*b.Confidence; wa != wb {
			return wa > wb
		}
		return a.ID < b.ID
	})
}
