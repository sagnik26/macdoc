// Package planner turns opportunity IDs (never paths) into a sealed,
// expiring dry-run plan (docs/SAFETY_MODEL.md §4.1). It is read-only apart
// from writing plan files into macdoc's own state directory: it can't
// delete, move or run anything. internal/executor (Phase 6) acts only on
// plans that pass Verify.
package planner

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
)

const (
	// TTL is how long a plan stays valid.
	TTL = 30 * time.Minute
	// Oldest is the oldest scan a plan may be built from.
	Oldest = 6 * time.Hour
)

// Selector says which opportunities to plan. Exactly one of IDs, Tier or
// Recipe is set.
type Selector struct {
	IDs    []string   // opportunity IDs, "O-" prefix optional, any case
	Tier   model.Risk // every opportunity of this risk tier
	Recipe string     // every opportunity of this recipe
}

// Request describes the selector as a user would type it.
func (s Selector) Request() string {
	switch {
	case len(s.IDs) > 0:
		return strings.Join(s.IDs, " ")
	case s.Tier == model.RiskSafe:
		return "--safe"
	case s.Tier != "":
		return "--tier " + strings.ToLower(string(s.Tier))
	}
	return "--recipe " + s.Recipe
}

// pattern is an opportunity ID as users type it.
var pattern = regexp.MustCompile(`(?i)^(o-)?[a-z2-7]{6,26}$`)

// Normalize turns an opportunity ID as typed (any case, "O-" optional,
// surrounding whitespace) into its canonical form "O-abcdef". It reports false
// for anything else, including non-ASCII look-alikes that case folding would
// let through (U+017F "ſ", the Kelvin sign).
func Normalize(a string) (string, bool) {
	a = strings.TrimSpace(a)
	for i := 0; i < len(a); i++ {
		if a[i] >= 0x80 {
			return "", false
		}
	}
	if !pattern.MatchString(a) {
		return "", false
	}
	return "O-" + strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(a, "O-"), "o-")), true
}

// Parse normalizes opportunity IDs, drops duplicates and rejects anything
// else, paths in particular: plans take IDs, never paths (docs/adr/0002-ids-not-paths.md).
func Parse(args []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, a := range args {
		a = strings.TrimSpace(a)
		if strings.ContainsAny(a, `/\~.`) {
			return nil, fmt.Errorf("%q looks like a path: plans take suggestion IDs (O-…), never paths", a)
		}
		id, ok := Normalize(a)
		if !ok {
			return nil, fmt.Errorf("%q isn't a suggestion ID (they look like O-ab12cd; see macdoc reclaim)", a)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// Input is what Build plans from: the latest complete scan's ranked
// opportunities.
type Input struct {
	ScanID        string
	ScanFinished  time.Time
	Opportunities []model.Opportunity
	Catalog       *recipes.Catalog
	Policy        *safety.Policy
	Now           time.Time
	TTL           time.Duration // 0: TTL
	Origin        model.Origin
	// Excluded reports a recipe ID the person excluded in settings. It can
	// only refuse items, never add any.
	Excluded func(recipeID string) bool
}

// Refusal is an opportunity that was asked for but left out, and why.
type Refusal struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Reason string `json:"reason"`
}

// ReasonNotInScan is the refusal for an ID the latest scan doesn't have.
const ReasonNotInScan = "not in the latest scan (IDs change after each scan; see macdoc reclaim)"

// ReasonExcluded is the refusal for an item whose recipe is excluded in settings.
const ReasonExcluded = "excluded in your settings"

// ReasonPrivilegedFromClaude is the refusal for a step that needs
// administrator rights in a plan Claude proposed: only a person creates those
// (ADR 0009).
const ReasonPrivilegedFromClaude = "it needs administrator rights, and plans Claude proposes never include those; make the plan yourself in Terminal"

// Errors from Build.
var (
	ErrStaleScan = errors.New("the latest scan is too old to plan from")
	ErrNothing   = errors.New("nothing to plan")
)

// Build selects opportunities and checks each target as it is now: a
// quarantine target must still exist, not be a symlink, have the same
// canonical path and pass the recipe allowlist and the protected-path rules;
// a native command's tool must be installed. It records each target's
// fingerprint so apply can tell if anything changed. Items that fail are
// refused with a reason, never "fixed up". The plan has no ID or seal yet:
// Save assigns both.
func Build(in Input, sel Selector) (model.Plan, []Refusal, error) {
	if in.TTL <= 0 {
		in.TTL = TTL
	}
	if in.Origin == "" {
		in.Origin = model.OriginCLI
	}
	if age := in.Now.Sub(in.ScanFinished); age > Oldest {
		return model.Plan{}, nil, fmt.Errorf(`%w (%d hours); run "macdoc scan" first`, ErrStaleScan, int(age.Hours()))
	}
	picked, refused := choose(in.Opportunities, sel, in.Excluded)

	plan := model.Plan{
		SchemaVersion: model.SchemaVersion,
		CreatedAt:     in.Now.UTC().Round(0),
		ExpiresAt:     in.Now.Add(in.TTL).UTC().Round(0),
		Origin:        in.Origin,
		ScanID:        in.ScanID,
		Request:       sel.Request(),
		Items:         []model.PlanItem{},
	}
	for _, op := range picked {
		item, reason := item(in, op)
		if reason != "" {
			refused = append(refused, Refusal{ID: op.ID, Title: op.Title, Reason: reason})
			continue
		}
		plan.Items = append(plan.Items, item)
		if item.BytesSource != model.SourceUnknown {
			plan.TotalBytes += item.ExpectedBytes
		}
	}
	order(plan.Items)
	if len(plan.Items) == 0 {
		return plan, refused, ErrNothing
	}
	return plan, refused, nil
}

// choose applies the selector. IDs keep the order given; unknown IDs and
// advice-only items are refused.
func choose(ops []model.Opportunity, sel Selector, excluded func(string) bool) ([]model.Opportunity, []Refusal) {
	var picked []model.Opportunity
	var refused []Refusal
	consider := func(op model.Opportunity) {
		switch {
		case excluded != nil && excluded(op.RecipeID):
			refused = append(refused, Refusal{ID: op.ID, Title: op.Title, Reason: ReasonExcluded})
		case op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise:
			refused = append(refused, Refusal{ID: op.ID, Title: op.Title, Reason: "advice only: macdoc takes no action on it"})
		case op.Risk == model.RiskBlocked:
			refused = append(refused, Refusal{ID: op.ID, Title: op.Title, Reason: "blocked by the safety rules"})
		default:
			picked = append(picked, op)
		}
	}
	if len(sel.IDs) > 0 {
		byID := map[string]model.Opportunity{}
		for _, op := range ops {
			byID[strings.ToLower(op.ID)] = op
		}
		for _, id := range sel.IDs {
			op, ok := byID[strings.ToLower(id)]
			if !ok {
				refused = append(refused, Refusal{ID: id, Reason: ReasonNotInScan})
				continue
			}
			consider(op)
		}
		return picked, refused
	}
	for _, op := range ops {
		if (sel.Tier != "" && op.Risk == sel.Tier) || (sel.Recipe != "" && op.RecipeID == sel.Recipe) {
			consider(op)
		}
	}
	return picked, refused
}

// item checks one opportunity and turns it into a plan item, or
// returns why it can't be planned.
func item(in Input, op model.Opportunity) (model.PlanItem, string) {
	r, ok := in.Catalog.Get(op.RecipeID)
	if !ok {
		return model.PlanItem{}, "its recipe isn't in this version of macdoc"
	}
	if r.Method != op.Method {
		return model.PlanItem{}, "its method doesn't match its recipe"
	}
	item := model.PlanItem{
		OpportunityID: op.ID,
		RecipeID:      op.RecipeID,
		Title:         op.Title,
		Target:        op.Target,
		ExpectedBytes: op.Bytes.Value,
		BytesSource:   op.Bytes.Source,
		Risk:          op.Risk,
		RiskReason:    op.RiskReason,
		Method:        op.Method,
		Preconditions: append([]string(nil), r.Preconditions...),
		Restore:       op.Restore,
	}
	if item.BytesSource == "" || item.BytesSource == model.SourceUnknown {
		item.BytesSource, item.ExpectedBytes = model.SourceUnknown, 0
	}
	switch op.Method {
	case model.MethodNative:
		if !recipes.MatchesNative(r, op.Command) {
			return item, "its command doesn't match the recipe's"
		}
		if !recipes.ToolInstalled(op.Command[0]) {
			return item, op.Command[0] + " isn't installed any more"
		}
		item.Argv = append([]string(nil), op.Command...)
		if r.Privileged {
			if in.Origin == model.OriginClaude {
				return item, ReasonPrivilegedFromClaude
			}
			if !recipes.ArgvIn(item.Argv, recipes.PrivilegedArgvAllowed) {
				return item, "its command isn't on the privileged allow-list"
			}
			item.Privileged = true
		}
	case model.MethodQuarantine:
		fp, reason := target(in.Policy, op.Target, r.Allow)
		if reason != "" {
			return item, reason
		}
		item.Fingerprint = fp
	default:
		return item, "unknown method"
	}
	return item, ""
}

// target re-checks a quarantine target now and fingerprints it.
func target(pol *safety.Policy, target string, allow []string) (model.Fingerprint, string) {
	fi, err := os.Lstat(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return model.Fingerprint{}, "it no longer exists"
	case err != nil:
		return model.Fingerprint{}, "it can't be read: " + err.Error()
	case fi.Mode()&os.ModeSymlink != 0:
		return model.Fingerprint{}, "it is now a symlink"
	}
	c, err := safety.Canonicalize(target)
	if err != nil || c != target {
		return model.Fingerprint{}, "its path changed since the scan"
	}
	if err := pol.CheckTarget(target, allow); err != nil {
		return model.Fingerprint{}, "the safety rules refuse it: " + err.Error()
	}
	return Fingerprint(fi), ""
}

// Fingerprint identifies a filesystem entry: device, inode, modification
// time and the entry's own allocated bytes.
func Fingerprint(fi os.FileInfo) model.Fingerprint {
	fp := model.Fingerprint{MtimeNS: fi.ModTime().UnixNano()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		fp.Dev = uint64(st.Dev) //nolint:gosec // G115: device numbers are non-negative
		fp.Ino = st.Ino
		fp.AllocBytes = st.Blocks * 512
	}
	return fp
}

// rank orders plan items: SAFE, CAUTION, RISKY.
func rank(r model.Risk) int {
	switch r {
	case model.RiskSafe:
		return 0
	case model.RiskCaution:
		return 1
	}
	return 2
}

// order groups items by tier, biggest first, then by ID.
func order(items []model.PlanItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if ta, tb := rank(a.Risk), rank(b.Risk); ta != tb {
			return ta < tb
		}
		if a.ExpectedBytes != b.ExpectedBytes {
			return a.ExpectedBytes > b.ExpectedBytes
		}
		return a.OpportunityID < b.OpportunityID
	})
}
