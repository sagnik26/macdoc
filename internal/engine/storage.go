package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/probe"
	"github.com/sagnik26/macdoc/internal/store"
)

// StorageOptions configures Storage.
type StorageOptions struct {
	OpportunityOptions
	Large bool // also list big items no recipe covers
	// Precomputed, if set, is used instead of running the detectors again
	// (the full overview already has the opportunities).
	Precomputed *OpportunityResult
}

// CategoryTotal sums the opportunities of one category.
type CategoryTotal struct {
	Category     string               `json:"category"`
	Bytes        int64                `json:"bytes"` // known bytes only
	Count        int                  `json:"count"`
	UnknownCount int                  `json:"unknown_count,omitempty"` // items whose size is unknown
	ByRisk       map[model.Risk]int64 `json:"by_risk"`
}

// SystemDataPart is one piece of what macOS lumps into "System Data".
type SystemDataPart struct {
	Name    string      `json:"name"`
	Bytes   model.Bytes `json:"bytes"`
	Explain string      `json:"explain"`
}

// StorageResult is the data behind `macdoc reclaim --categories`.
type StorageResult struct {
	Scan       model.ScanInfo   `json:"scan"`
	ScanAge    string           `json:"scan_age"`
	Categories []CategoryTotal  `json:"categories"`
	SystemData []SystemDataPart `json:"system_data"`
	Large      []model.Node     `json:"large,omitempty"` // advise-only: no recipe covers them
	Warnings   []string         `json:"warnings,omitempty"`
}

// Paths in the scan that are part of "System Data".
const (
	varFoldersPath   = "/private/var/folders"
	coreSimulatorDir = "/Library/Developer/CoreSimulator"
	largeMinBytes    = 1_000_000_000
	largeLimit       = 20
)

// Storage builds the category breakdown, decodes "System Data" and, with
// Large, lists big items that no recipe covers. It is read-only.
func Storage(ctx context.Context, o StorageOptions) (StorageResult, error) {
	var opp OpportunityResult
	if o.Precomputed != nil {
		opp = *o.Precomputed
	} else {
		var err error
		if opp, err = Opportunities(ctx, o.OpportunityOptions); err != nil {
			return StorageResult{}, err
		}
	}
	res := StorageResult{Scan: opp.Scan, ScanAge: opp.ScanAge, Warnings: opp.Warnings,
		Categories: Categories(opp.Opportunities)}

	storePath := o.StorePath
	if storePath == "" {
		storePath = store.DefaultPath(o.Home)
	}
	st, err := store.Open(storePath)
	if err != nil {
		return StorageResult{}, err
	}
	defer func() { _ = st.Close() }()

	res.SystemData = systemData(ctx, o, st, opp.Scan, &res.Warnings)

	if o.Large {
		nodes, err := st.Nodes(opp.Scan.ID, store.Query{})
		if err != nil {
			return StorageResult{}, err
		}
		res.Large = largeUncovered(nodes, opp.Opportunities)
	}
	return res, nil
}

// Categories groups opportunities by category, biggest known total first.
func Categories(ops []model.Opportunity) []CategoryTotal {
	idx := map[string]int{}
	out := []CategoryTotal{}
	for _, op := range ops {
		i, ok := idx[op.Category]
		if !ok {
			i = len(out)
			idx[op.Category] = i
			out = append(out, CategoryTotal{Category: op.Category, ByRisk: map[model.Risk]int64{}})
		}
		c := &out[i]
		c.Count++
		if op.Bytes.Source == model.SourceUnknown {
			c.UnknownCount++
			continue
		}
		c.Bytes += op.Bytes.Value
		c.ByRisk[op.Risk] += op.Bytes.Value
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Category < out[j].Category
	})
	return out
}

func unknownBytes(reason string) model.Bytes {
	return model.Bytes{Source: model.SourceUnknown, Reason: reason}
}

// systemData decodes the parts of "System Data" macdoc can attribute.
// Anything it can't read is reported as unknown, never as zero.
func systemData(ctx context.Context, o StorageOptions, st *store.Store, scan model.ScanInfo, warnings *[]string) []SystemDataPart {
	var (
		disk    probe.Disk
		snaps   probe.Snapshots
		diskErr = errors.New("no command runner")
		snapErr = diskErr
	)
	if o.Runner != nil {
		disk, diskErr = probe.CollectDisk(ctx, o.Runner)
		snaps, snapErr = probe.CollectSnapshots(ctx, o.Runner)
	}
	if diskErr != nil {
		*warnings = append(*warnings, "disk: "+diskErr.Error())
	}
	if snapErr != nil {
		*warnings = append(*warnings, "snapshots: "+snapErr.Error())
	}

	var parts []SystemDataPart

	snapPart := SystemDataPart{Name: "Local Time Machine snapshots",
		Bytes:   unknownBytes("macOS doesn't report how much space snapshots hold"),
		Explain: "Snapshots keep deleted files' space in use until macOS thins them, which it does automatically when space runs low."}
	if snapErr != nil {
		snapPart.Bytes.Reason = "snapshot list unavailable"
	} else {
		snapPart.Name = fmt.Sprintf("Local Time Machine snapshots (%d)", snaps.Count)
	}
	parts = append(parts, snapPart)

	vm := SystemDataPart{Name: "Swap and sleep image (VM volume)",
		Bytes:   unknownBytes("VM volume size unavailable"),
		Explain: "Swap grows when RAM is overcommitted and shrinks after apps quit or a restart; the sleep image matches RAM size."}
	data := unknownBytes("Data volume size unavailable")
	for _, v := range disk.Volumes {
		switch {
		case slices.Contains(v.Roles, "VM"):
			vm.Bytes = model.Bytes{Value: v.UsedBytes, Source: model.SourceTool}
		case slices.Contains(v.Roles, "Data"):
			data = model.Bytes{Value: v.UsedBytes, Source: model.SourceTool}
		}
	}
	parts = append(parts, vm)

	purge := SystemDataPart{Name: "Purgeable space", Bytes: disk.PurgeableBytes,
		Explain: "Caches and iCloud copies macOS can remove on its own when space is needed."}
	if diskErr != nil && disk.ContainerRef == "" {
		purge.Bytes = unknownBytes("disk information unavailable")
	}
	parts = append(parts, purge)

	hidden := SystemDataPart{Name: "Not visible to the scan",
		Bytes:   unknownBytes("Data volume size unavailable"),
		Explain: "Space APFS reports as used that the scan could not see: locations needing Full Disk Access, plus system-managed indexes and metadata."}
	if data.Source != model.SourceUnknown {
		gap := max(0, data.Value-scan.TotalBytes)
		hidden.Bytes = model.Bytes{Value: gap, Source: model.SourceEstimated, Reason: "Data volume used minus scan total"}
	}
	parts = append(parts, hidden)

	parts = append(parts,
		scanPart(st, scan.ID, varFoldersPath, "Temporary and per-app cache files",
			"Per-user temporary files and caches under /private/var/folders. macOS clears them at restart and as needed; macdoc never acts on them."),
		scanPart(st, scan.ID, coreSimulatorDir, "Shared simulator data",
			"System-wide simulator runtimes and caches installed by Xcode. Manage them with Xcode or `xcrun simctl`."),
	)
	return parts
}

// scanPart sizes one path from the scan, or reports it as unknown.
func scanPart(st *store.Store, scanID, path, name, explain string) SystemDataPart {
	part := SystemDataPart{Name: name + " (" + path + ")", Explain: explain,
		Bytes: unknownBytes("not in the latest scan")}
	nodes, err := st.Nodes(scanID, store.Query{Prefix: path, MaxDepth: 1})
	if err != nil {
		part.Bytes.Reason = err.Error()
		return part
	}
	for _, n := range nodes {
		if strings.EqualFold(n.Path, path) {
			part.Bytes = model.Bytes{Value: n.Bytes, Source: model.SourceMeasured}
		}
	}
	return part
}

// largeUncovered returns the biggest "where the space is" nodes that are
// not equal to or below any opportunity target. The System Data folders are
// skipped too: they are already shown, with an explanation, above.
func largeUncovered(nodes []model.Node, ops []model.Opportunity) []model.Node {
	targets := map[string]bool{}
	for _, op := range ops {
		if op.Target != "" {
			targets[foldPath(op.Target)] = true
		}
	}
	shown := map[string]bool{foldPath(varFoldersPath): true, foldPath(coreSimulatorDir): true}
	var out []model.Node
	for _, n := range Largest(nodes, 0, largeMinBytes) {
		p := foldPath(n.Path)
		if shown[p] || targets[p] || HasAncestor(targets, p) {
			continue
		}
		out = append(out, n)
		if len(out) == largeLimit {
			break
		}
	}
	return out
}
