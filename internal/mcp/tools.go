package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"macdoc/internal/engine"
	"macdoc/internal/model"
	"macdoc/internal/planner"
	"macdoc/internal/projects"
	"macdoc/internal/store"
)

// ToolNames are every tool the server offers. A test pins this list: adding
// a tool is a deliberate, reviewed change, and none may apply, delete, move,
// restore, purge, run a command, take a path or change settings.
var ToolNames = []string{
	"get_system_overview", "get_storage_breakdown", "list_opportunities", "explain_opportunity",
	"list_projects", "diagnose_slowness", "get_growth_history", "refresh_scan",
	"create_cleanup_plan", "get_plan", "get_audit_log",
}

var (
	falseP        = new(bool)
	readOnly      = &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: falseP}
	opportunityRe = regexp.MustCompile(`^(?i)(O-)?[a-z2-7]{6}$`)
)

const (
	maxPlanIDs     = 50
	defaultListMax = 50
	maxList        = 200
	maxAudit       = 500
)

type noInput struct{}

type storageIn struct {
	IncludeLarge bool `json:"include_large,omitempty" jsonschema:"also list big items no recipe covers (advice only)"`
}

type opportunitiesIn struct {
	Risk      []string `json:"risk,omitempty" jsonschema:"only these risk tiers: SAFE, CAUTION, RISKY, ADVISE"`
	Toolchain string   `json:"toolchain,omitempty" jsonschema:"only this toolchain, e.g. xcode, android, node, docker, python, ai, homebrew, system"`
	MinBytes  int64    `json:"min_bytes,omitempty" jsonschema:"only items of at least this many bytes (items of unknown size are kept)"`
	IdleDays  int      `json:"idle_days,omitempty" jsonschema:"only items idle at least this many days"`
	Limit     int      `json:"limit,omitempty" jsonschema:"at most this many items (default 50, max 200)"`
}

type idIn struct {
	ID string `json:"id" jsonschema:"an ID from a previous result"`
}

type projectsIn struct {
	IdleDays int `json:"idle_days,omitempty" jsonschema:"only projects idle at least this many days"`
}

type growthIn struct {
	Days int `json:"days,omitempty" jsonschema:"compare with a scan about this many days old (default 30)"`
}

type planIn struct {
	OpportunityIDs []string `json:"opportunity_ids" jsonschema:"opportunity IDs (O-…) from list_opportunities, at most 50"`
}

type auditIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"at most this many recent records (default 50, max 500)"`
}

func (s *Server) register(srv *sdk.Server) {
	sdk.AddTool(srv, &sdk.Tool{Name: "get_system_overview", Annotations: readOnly,
		Description: "Hardware, macOS version, disk and free space, purgeable space, memory pressure, swap, local snapshots, the most important findings, and the age of the latest scan."},
		s.overview)
	sdk.AddTool(srv, &sdk.Tool{Name: "get_storage_breakdown", Annotations: readOnly,
		Description: `Reclaimable space by category and risk, and "System Data" decoded, from the latest scan.`},
		s.storage)
	sdk.AddTool(srv, &sdk.Tool{Name: "list_opportunities", Annotations: readOnly,
		Description: "Cleanup suggestions from the latest scan, ranked by safety then size: ID, title, size (with its source), risk, idle days, how it comes back (restore cost) and the action. Use the IDs with explain_opportunity and create_cleanup_plan."},
		s.opportunities)
	sdk.AddTool(srv, &sdk.Tool{Name: "explain_opportunity", Annotations: readOnly,
		Description: "One suggestion in detail: what it is, why it's there, risk (and why it was raised), method, preconditions, restore cost, and for ADVISE items the steps the user can follow by hand."},
		s.explain)
	sdk.AddTool(srv, &sdk.Tool{Name: "list_projects", Annotations: readOnly,
		Description: "The user's projects with build artifacts (node_modules, Pods, build folders, .venv, DerivedData…): idle days, artifact size and git state."},
		s.projects)
	sdk.AddTool(srv, &sdk.Tool{Name: "diagnose_slowness", Annotations: readOnly,
		Description: "Whether memory is what's slowing the Mac down: a verdict, the biggest memory users against RAM size with what each is, swap now and over the last 7 days, and low-disk risk. It only explains; nothing can quit or change an app."},
		s.slow)
	sdk.AddTool(srv, &sdk.Tool{Name: "get_growth_history", Annotations: readOnly,
		Description: "What grew between the latest scan and one about N days old: per area (Xcode, Gradle, Docker, …) and the folders that grew most. Folders a scan didn't store are given as bounds."},
		s.growth)
	sdk.AddTool(srv, &sdk.Tool{Name: "refresh_scan",
		Annotations: &sdk.ToolAnnotations{IdempotentHint: true, DestructiveHint: falseP, OpenWorldHint: falseP},
		Description: "Starts a fresh read-only disk scan in the background (about 1–2 minutes at low priority), or reports the one running or just finished. Call it again to check. A scan from here can see less than one from Terminal, because it has the Claude app's file permissions; prefer the existing scan unless it's old."},
		s.refreshScan)
	sdk.AddTool(srv, &sdk.Tool{Name: "create_cleanup_plan",
		Annotations: &sdk.ToolAnnotations{DestructiveHint: falseP, OpenWorldHint: falseP},
		Description: "Proposes a cleanup: seals a dry-run plan of the given opportunity IDs and returns its ID, contents and the exact command for the USER to run in Terminal. It changes nothing on the Mac and you can't apply it: tell the user the plan and the command. They confirm each item in Terminal."},
		s.createPlan)
	sdk.AddTool(srv, &sdk.Tool{Name: "get_plan", Annotations: readOnly,
		Description: "A plan's contents and status (pending, applied, expired, invalid). Use it after the user ran macdoc apply to see what happened, with get_audit_log."},
		s.getPlan)
	sdk.AddTool(srv, &sdk.Tool{Name: "get_audit_log", Annotations: readOnly,
		Description: "Recent entries of macdoc's audit log: plans made, applies, each item done, skipped or failed (with the reason), restores and purges."},
		s.audit)
}

func (s *Server) overview(ctx context.Context, _ *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, any, error) {
	out := map[string]any{"health": engine.Doctor(ctx, s.o.Runner)}
	info, age, err := engine.LastScan(s.o.Home, s.o.Now())
	switch {
	case err == nil:
		out["scan"], out["scan_age"] = info, age
	case errors.Is(err, store.ErrNoScan):
		out["scan"] = nil
		out["scan_age"] = "no scan yet: call refresh_scan, or ask the user to run macdoc doctor in Terminal (better: it has Full Disk Access)"
	default:
		return toolError(err)
	}
	return result(out, nil)
}

func (s *Server) storage(ctx context.Context, _ *sdk.CallToolRequest, in storageIn) (*sdk.CallToolResult, any, error) {
	res, err := engine.Storage(ctx, engine.StorageOptions{OpportunityOptions: s.opp(), Large: in.IncludeLarge})
	if err != nil {
		return toolError(err)
	}
	return result(res, res.Warnings)
}

func (s *Server) opportunities(ctx context.Context, _ *sdk.CallToolRequest, in opportunitiesIn) (*sdk.CallToolResult, any, error) {
	limit := in.Limit
	switch {
	case limit <= 0:
		limit = defaultListMax
	case limit > maxList:
		limit = maxList
	}
	var risks []model.Risk
	for _, r := range in.Risk {
		risk := model.Risk(strings.ToUpper(strings.TrimSpace(r)))
		if !slices.Contains(engine.TierOrder, risk) {
			return toolError(fmt.Errorf("unknown risk %q: use SAFE, CAUTION, RISKY or ADVISE", r))
		}
		risks = append(risks, risk)
	}
	res, err := engine.Opportunities(ctx, s.opp())
	if err != nil {
		return toolError(err)
	}
	ops := []model.Opportunity{}
	matched := 0
	for _, op := range res.Opportunities {
		switch {
		case len(risks) > 0 && !slices.Contains(risks, op.Risk),
			in.Toolchain != "" && !strings.EqualFold(op.Toolchain, in.Toolchain),
			in.MinBytes > 0 && op.Bytes.Source != model.SourceUnknown && op.Bytes.Value < in.MinBytes,
			in.IdleDays > 0 && op.IdleDays < in.IdleDays:
			continue
		}
		matched++
		if len(ops) < limit {
			ops = append(ops, op)
		}
	}
	return result(map[string]any{"scan": res.Scan, "scan_age": res.ScanAge, "totals": res.Totals,
		"matched": matched, "opportunities": ops}, res.Warnings)
}

func (s *Server) explain(ctx context.Context, _ *sdk.CallToolRequest, in idIn) (*sdk.CallToolResult, any, error) {
	if !opportunityRe.MatchString(strings.TrimSpace(in.ID)) {
		return toolError(fmt.Errorf("%q isn't an opportunity ID (they look like O-ab12cd)", in.ID))
	}
	id := "O-" + strings.ToLower(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(in.ID)), "O-"))
	res, err := engine.Opportunities(ctx, s.opp())
	if err != nil {
		return toolError(err)
	}
	for _, op := range res.Opportunities {
		if op.ID != id {
			continue
		}
		out := map[string]any{"opportunity": op, "base_risk": op.Risk, "scan_age": res.ScanAge}
		if base, raised := engine.RaisedFrom(op, s.o.Catalog); raised {
			out["base_risk"] = base
		}
		if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise {
			out["can_plan"] = false
			out["how_to"] = "macdoc takes no action on advice-only items. Relay the steps to the user."
		} else {
			out["can_plan"] = true
		}
		return result(out, res.Warnings)
	}
	return toolError(fmt.Errorf("%s isn't in the latest scan; IDs change after a new scan, so call list_opportunities again", id))
}

func (s *Server) projects(ctx context.Context, _ *sdk.CallToolRequest, in projectsIn) (*sdk.CallToolResult, any, error) {
	res, err := engine.Projects(ctx, s.opp())
	if err != nil {
		return toolError(err)
	}
	kept := []projects.Project{}
	for _, p := range res.Projects {
		if p.ArtifactBytes < 1_000_000 || (in.IdleDays > 0 && p.IdleDays < in.IdleDays) {
			continue
		}
		kept = append(kept, p)
	}
	res.Projects = kept
	return result(res, res.Warnings)
}

func (s *Server) slow(ctx context.Context, _ *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, any, error) {
	res := engine.Slow(ctx, engine.SlowOptions{Home: s.o.Home, Runner: s.o.Runner, Now: s.o.Now()})
	return result(res, slices.Concat(res.Health.Report.Warnings, res.Warnings))
}

func (s *Server) growth(_ context.Context, _ *sdk.CallToolRequest, in growthIn) (*sdk.CallToolResult, any, error) {
	if in.Days < 0 || in.Days > 365 {
		return toolError(errors.New("days must be between 1 and 365"))
	}
	res, err := engine.Trend(engine.TrendOptions{Home: s.o.Home, Days: in.Days})
	switch {
	case errors.Is(err, engine.ErrNoHistory), errors.Is(err, store.ErrNoScan):
		return toolError(errors.New("there aren't two scans to compare yet; growth history builds up as the user rescans over days"))
	case err != nil:
		return toolError(err)
	}
	return result(res, nil)
}

func (s *Server) refreshScan(_ context.Context, _ *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := &s.scan
	switch {
	case j.running:
		return result(map[string]any{"status": "running", "started_at": j.started,
			"elapsed": s.o.Now().Sub(j.started).Round(time.Second).String()}, nil)
	case !j.finished.IsZero() && s.o.Now().Sub(j.finished) < 10*time.Minute:
		out := map[string]any{"status": "done", "finished_at": j.finished}
		if j.err != nil {
			out["status"], out["error"] = "failed", j.err.Error()
		} else if j.summary != nil {
			out["scan"], out["unmeasured_count"] = j.summary.Info, len(j.summary.Unmeasured)
			out["data_volume_used"] = j.summary.DataVolumeUsed
		}
		return result(out, nil)
	}
	*j = scanJob{running: true, started: s.o.Now()}
	go s.runScan()
	return result(map[string]any{"status": "started", "started_at": j.started,
		"next": "call refresh_scan again in about a minute to see when it's done"}, nil)
}

func (s *Server) runScan() {
	sum, err := engine.Scan(s.base, engine.ScanOptions{Home: s.o.Home, Version: s.o.Version, Runner: s.o.Runner, Root: s.o.ScanRoot})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scan.running, s.scan.finished, s.scan.err = false, s.o.Now(), err
	if err == nil {
		s.scan.summary = &sum
	}
}

func (s *Server) createPlan(ctx context.Context, _ *sdk.CallToolRequest, in planIn) (*sdk.CallToolResult, any, error) {
	if len(in.OpportunityIDs) == 0 || len(in.OpportunityIDs) > maxPlanIDs {
		return toolError(fmt.Errorf("give between 1 and %d opportunity IDs", maxPlanIDs))
	}
	for _, id := range in.OpportunityIDs {
		if !opportunityRe.MatchString(strings.TrimSpace(id)) {
			return toolError(fmt.Errorf("%q isn't an opportunity ID (they look like O-ab12cd); plans take IDs only, never paths", id))
		}
	}
	res, err := engine.CreatePlan(ctx, engine.PlanOptions{OpportunityOptions: s.opp(),
		Selector: planner.Selector{IDs: in.OpportunityIDs}, Origin: model.OriginClaude})
	switch {
	case errors.Is(err, planner.ErrNothing):
		var why []string
		for _, r := range res.Refused {
			why = append(why, r.ID+": "+r.Reason)
		}
		return toolError(fmt.Errorf("nothing could be planned: %s", strings.Join(why, "; ")))
	case errors.Is(err, planner.ErrStaleScan), errors.Is(err, engine.ErrNoScan):
		return toolError(fmt.Errorf("%w: call refresh_scan, or ask the user to run macdoc doctor scan in Terminal, then list_opportunities again", err))
	case err != nil:
		return toolError(err)
	}
	p := res.Plan
	byRisk := map[model.Risk]int{}
	unknown := 0
	type item struct {
		ID      string        `json:"id"`
		Title   string        `json:"title"`
		Risk    model.Risk    `json:"risk"`
		Bytes   model.Bytes   `json:"bytes"`
		Action  string        `json:"action"`
		Restore model.Restore `json:"restore"`
	}
	items := []item{}
	for _, it := range p.Items {
		byRisk[it.Risk]++
		if it.BytesSource == model.SourceUnknown {
			unknown++
		}
		action := "delete " + it.Target
		if it.Method == model.MethodNative {
			action = "run the tool's own command: " + strings.Join(it.Argv, " ")
		}
		items = append(items, item{ID: it.OpportunityID, Title: it.Title, Risk: it.Risk,
			Bytes: model.Bytes{Value: it.ExpectedBytes, Source: it.BytesSource}, Action: action, Restore: it.Restore})
	}
	summary := map[string]any{"items": len(p.Items), "bytes": p.TotalBytes, "by_risk": byRisk}
	if unknown > 0 {
		summary["unknown_size_items"] = unknown
	}
	return result(map[string]any{
		"plan_id":     p.ID,
		"expires_at":  p.ExpiresAt,
		"summary":     summary,
		"items":       items,
		"refused":     res.Refused,
		"user_action": "Run in Terminal:  macdoc apply " + p.ID,
		"note":        "Claude cannot apply plans. The user reviews the dry run and confirms each item in Terminal; removed items are deleted at once.",
	}, res.Warnings)
}

func (s *Server) getPlan(ctx context.Context, _ *sdk.CallToolRequest, in idIn) (*sdk.CallToolResult, any, error) {
	id := strings.ToUpper(strings.TrimSpace(in.ID))
	if !planner.ValidID(id) {
		return toolError(fmt.Errorf("%q isn't a plan ID (they look like P-7F3K)", in.ID))
	}
	res, err := engine.ShowPlan(ctx, s.opp(), id)
	if err != nil {
		return toolError(err)
	}
	return result(res, res.Warnings)
}

func (s *Server) audit(_ context.Context, _ *sdk.CallToolRequest, in auditIn) (*sdk.CallToolResult, any, error) {
	limit := in.Limit
	switch {
	case limit <= 0:
		limit = defaultListMax
	case limit > maxAudit:
		limit = maxAudit
	}
	res, err := engine.AuditLog(s.opp(), limit)
	if err != nil {
		return toolError(err)
	}
	return result(res, nil)
}
