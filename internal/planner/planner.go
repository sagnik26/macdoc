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
	// DefaultTTL is how long a plan stays valid.
	DefaultTTL = 30 * time.Minute
	// MaxScanAge is the oldest scan a plan may be built from.
	MaxScanAge = 6 * time.Hour
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

// idRe is an opportunity ID as users type it.
var idRe = regexp.MustCompile(`(?i)^(o-)?[a-z2-7]{6,26}$`)

// NormalizeID turns an opportunity ID as typed (any case, "O-" optional,
// surrounding whitespace) into its canonical form "O-abcdef". It reports false
// for anything else, including non-ASCII look-alikes that case folding would
// let through (U+017F "ſ", the Kelvin sign).
func NormalizeID(a string) (string, bool) {
	a = strings.TrimSpace(a)
	for i := 0; i < len(a); i++ {
		if a[i] >= 0x80 {
			return "", false
		}
	}
	if !idRe.MatchString(a) {
		return "", false
	}
	return "O-" + strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(a, "O-"), "o-")), true
}

// ParseIDs normalizes opportunity IDs, drops duplicates and rejects anything
// else, paths in particular: plans take IDs, never paths (docs/adr/0002-ids-not-paths.md).
func ParseIDs(args []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, a := range args {
		a = strings.TrimSpace(a)
		if strings.ContainsAny(a, `/\~.`) {
			return nil, fmt.Errorf("%q looks like a path: plans take suggestion IDs (O-…), never paths", a)
		}
		id, ok := NormalizeID(a)
		if !ok {
			return nil, fmt.Errorf("%q isn't a suggestion ID (they look like O-ab12cd; see macdoc doctor wins)", a)
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
	TTL           time.Duration // 0: DefaultTTL
	Origin        model.Origin
}

// Refusal is an opportunity that was asked for but left out, and why.
type Refusal struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Reason string `json:"reason"`
}

// ReasonNotInScan is the refusal for an ID the latest scan doesn't have.
const ReasonNotInScan = "not in the latest scan (IDs change after each scan; see macdoc doctor wins)"

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
		in.TTL = DefaultTTL
	}
	if in.Origin == "" {
		in.Origin = model.OriginCLI
	}
	if age := in.Now.Sub(in.ScanFinished); age > MaxScanAge {
		return model.Plan{}, nil, fmt.Errorf(`%w (%d hours); run "macdoc doctor scan" first`, ErrStaleScan, int(age.Hours()))
	}
	picked, refused := selectOps(in.Opportunities, sel)

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
		item, reason := buildItem(in, op)
		if reason != "" {
			refused = append(refused, Refusal{ID: op.ID, Title: op.Title, Reason: reason})
			continue
		}
		plan.Items = append(plan.Items, item)
		if item.BytesSource != model.SourceUnknown {
			plan.TotalBytes += item.ExpectedBytes
		}
	}
	sortItems(plan.Items)
	if len(plan.Items) == 0 {
		return plan, refused, ErrNothing
	}
	return plan, refused, nil
}

// selectOps applies the selector. IDs keep the order given; unknown IDs and
// advice-only items are refused.
func selectOps(ops []model.Opportunity, sel Selector) ([]model.Opportunity, []Refusal) {
	var picked []model.Opportunity
	var refused []Refusal
	consider := func(op model.Opportunity) {
		switch {
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

// buildItem checks one opportunity and turns it into a plan item, or
// returns why it can't be planned.
func buildItem(in Input, op model.Opportunity) (model.PlanItem, string) {
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
	case model.MethodQuarantine:
		fp, reason := checkTarget(in.Policy, op.Target, r.Allow)
		if reason != "" {
			return item, reason
		}
		item.Fingerprint = fp
	default:
		return item, "unknown method"
	}
	return item, ""
}

// checkTarget re-checks a quarantine target now and fingerprints it.
func checkTarget(pol *safety.Policy, target string, allow []string) (model.Fingerprint, string) {
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

// tierRank orders plan items: SAFE, CAUTION, RISKY.
func tierRank(r model.Risk) int {
	switch r {
	case model.RiskSafe:
		return 0
	case model.RiskCaution:
		return 1
	}
	return 2
}

// sortItems groups items by tier, biggest first, then by ID.
func sortItems(items []model.PlanItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if ta, tb := tierRank(a.Risk), tierRank(b.Risk); ta != tb {
			return ta < tb
		}
		if a.ExpectedBytes != b.ExpectedBytes {
			return a.ExpectedBytes > b.ExpectedBytes
		}
		return a.OpportunityID < b.OpportunityID
	})
}
