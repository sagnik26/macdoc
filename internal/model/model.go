// Package model defines the core data types shared by the engine, the CLI and
// (in Phase C) the MCP layer. These types are contracts: changes go through
// the tech lead (see docs/ORCHESTRATION.md).
package model

import "time"

// SchemaVersion is the version of every JSON document macdoc emits.
const SchemaVersion = "1"

// Risk is the safety class of an opportunity (docs/SAFETY_MODEL.md §2).
type Risk string

const (
	RiskSafe    Risk = "SAFE"
	RiskCaution Risk = "CAUTION"
	RiskRisky   Risk = "RISKY"
	RiskAdvise  Risk = "ADVISE"
	RiskBlocked Risk = "BLOCKED"
)

// Source says where a number came from. Unknown is never shown as zero.
type Source string

const (
	SourceMeasured  Source = "measured"
	SourceTool      Source = "tool"
	SourceEstimated Source = "estimated"
	SourceUnknown   Source = "unknown"
)

// Bytes is a size together with its provenance.
type Bytes struct {
	Value  int64  `json:"value"`
	Source Source `json:"source"`
	Reason string `json:"reason,omitempty"` // why it is unknown or estimated
}

// Method is how an opportunity would be reclaimed.
type Method string

const (
	MethodNative     Method = "native"
	MethodQuarantine Method = "quarantine"
	MethodAdvise     Method = "advise"
)

// RestoreKind describes what it costs to get reclaimed data back.
type RestoreKind string

const (
	RestoreRebuild       RestoreKind = "rebuild"
	RestoreRedownload    RestoreKind = "redownload"
	RestoreIrreplaceable RestoreKind = "irreplaceable"
	RestoreNone          RestoreKind = "none"
)

// Restore is the cost of undoing a cleanup by regenerating the data.
type Restore struct {
	Kind            RestoreKind `json:"kind"`
	Note            string      `json:"note,omitempty"`
	RedownloadBytes int64       `json:"redownload_bytes,omitempty"`
}

// Opportunity is one reclaimable item found by a scan and matched to a recipe.
type Opportunity struct {
	ID         string  `json:"id"` // "O-xxxxxx", opaque and stable per scan
	ScanID     string  `json:"scan_id"`
	RecipeID   string  `json:"recipe_id"`
	Title      string  `json:"title"`
	Target     string  `json:"target"` // canonical path; display only outside the engine
	Bytes      Bytes   `json:"bytes"`
	Risk       Risk    `json:"risk"`
	Method     Method  `json:"method"`
	IdleDays   int     `json:"idle_days"` // -1 if unknown
	Restore    Restore `json:"restore"`
	Confidence float64 `json:"confidence"` // 0..1
	Explain    string  `json:"explain"`

	Category  string   `json:"category"`
	Toolchain string   `json:"toolchain"`
	Command   []string `json:"command,omitempty"` // native method: exact argv, {id} already substituted
	DryRun    []string `json:"dry_run,omitempty"` // the tool's own dry-run argv, if any
	Note      string   `json:"note,omitempty"`    // instance-specific detail from the detector
	// RiskReason says why Risk is above the recipe's base risk, e.g. "it
	// changed in the last 7 days" or "the project has uncommitted changes".
	RiskReason string `json:"risk_reason,omitempty"`
}

// Origin records who created a plan.
type Origin string

const (
	OriginCLI    Origin = "cli"
	OriginClaude Origin = "claude"
)

// Fingerprint identifies a filesystem target at plan time so the executor can
// detect changes before acting (docs/SAFETY_MODEL.md §4.3).
type Fingerprint struct {
	Dev        uint64 `json:"dev"`
	Ino        uint64 `json:"ino"`
	MtimeNS    int64  `json:"mtime_ns"`
	AllocBytes int64  `json:"alloc_bytes"`
}

// PlanItem is one action in a sealed plan.
type PlanItem struct {
	OpportunityID string      `json:"opportunity_id"`
	RecipeID      string      `json:"recipe_id"`
	Target        string      `json:"target"`
	ExpectedBytes int64       `json:"expected_bytes"`
	Risk          Risk        `json:"risk"`
	Method        Method      `json:"method"`
	Argv          []string    `json:"argv,omitempty"` // native method only
	Preconditions []string    `json:"preconditions,omitempty"`
	Fingerprint   Fingerprint `json:"fingerprint"`
}

// Plan is an immutable, hash-sealed dry-run plan.
type Plan struct {
	SchemaVersion string     `json:"schema_version"`
	ID            string     `json:"id"` // "P-XXXX"
	CreatedAt     time.Time  `json:"created_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	Origin        Origin     `json:"origin"`
	ScanID        string     `json:"scan_id"`
	Items         []PlanItem `json:"items"`
	TotalBytes    int64      `json:"total_bytes"`
	SHA256        string     `json:"sha256,omitempty"` // excluded from the hashed content
}

// Envelope wraps every --json output (docs/CLI_UX.md §4).
type Envelope struct {
	SchemaVersion string    `json:"schema_version"`
	Command       string    `json:"command"`
	GeneratedAt   time.Time `json:"generated_at"`
	Data          any       `json:"data"`
	Warnings      []string  `json:"warnings,omitempty"`
}
