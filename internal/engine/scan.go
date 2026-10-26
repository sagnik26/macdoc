package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/probe"
	"macdoc/internal/recipes"
	"macdoc/internal/scanner"
	"macdoc/internal/store"
)

// ScanOptions configures a stored scan. Zero values mean "real Data volume".
type ScanOptions struct {
	Home      string // required: the user's home (for store and quarantine paths)
	StorePath string // default store.DefaultPath(Home)
	Version   string // macdoc version recorded with the scan
	Workers   int
	Progress  func(scanner.Progress)
	// Runner, if set, is used to read the Data volume's used bytes so the
	// summary can say how much of it the scan could see.
	Runner platform.Runner

	// Root/MapPrefix override the scan root (tests). Empty means the Data
	// volume with firmlink mapping.
	Root      string
	MapPrefix string
}

// keepScans is how many complete scans are retained for trends.
const keepScans = 10

// nodeBatch is the AddNodes batch size.
const nodeBatch = 1000

// ScanSummary is what `macdoc scan` reports.
type ScanSummary struct {
	Info             model.ScanInfo     `json:"scan"`
	TopLevel         []model.Node       `json:"top_level"`  // direct children of the root
	Largest          []model.Node       `json:"largest"`    // biggest "where the space actually is" folders
	Unmeasured       []model.Unmeasured `json:"unmeasured"` //
	HardlinkLimitHit bool               `json:"hardlink_limit_hit,omitempty"`
	// DataVolumeUsed is what APFS reports as used on the Data volume (unknown
	// if it couldn't be read). The difference to Info.TotalBytes is space the
	// scan could not see: unreadable or skipped locations.
	DataVolumeUsed model.Bytes `json:"data_volume_used"`
}

// defaultScanLists returns Skip and Opaque lists for the real Data volume.
// Skip: system bookkeeping that isn't user data, swap (reported by doctor),
// and macdoc's own quarantine. Opaque: per-user temp areas measured as one
// bucket and never offered for action.
func defaultScanLists(root, home string) (skip, opaque []string) {
	d := func(p string) string { return filepath.Join(root, p) }
	skip = []string{
		d(".Spotlight-V100"), d(".fseventsd"), d(".DocumentRevisions-V100"),
		d("private/var/vm"),
		d(filepath.Join(home, "Library/Application Support/macdoc/quarantine")),
	}
	opaque = []string{d("private/var/folders"), d("private/var/db")}
	return skip, opaque
}

// Scan runs a full crawl and stores the result. The caller decides process
// priority (see scanner.SetBackgroundPriority).
func Scan(ctx context.Context, o ScanOptions) (ScanSummary, error) {
	if o.Home == "" {
		return ScanSummary{}, fmt.Errorf("scan: home directory is required")
	}
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	opts := scanner.Options{Root: o.Root, MapPrefix: o.MapPrefix, Workers: o.Workers, Progress: o.Progress}
	if opts.Root == "" {
		opts.Root, opts.MapPrefix = scanner.DataVolume, scanner.DataVolume
		opts.Skip, opts.Opaque = defaultScanLists(scanner.DataVolume, o.Home)
	}

	cat, err := recipes.Load()
	if err != nil {
		return ScanSummary{}, err
	}
	opts.Keep = recipes.KeepFunc(cat, o.Home)

	st, err := store.Open(o.StorePath)
	if err != nil {
		return ScanSummary{}, err
	}
	defer func() { _ = st.Close() }()

	id, err := st.BeginScan(opts.Root, o.Version, time.Now().UTC())
	if err != nil {
		return ScanSummary{}, err
	}
	res, scanErr := scanner.Scan(ctx, opts)
	status := model.ScanComplete
	if scanErr != nil || res.Incomplete {
		status = model.ScanIncomplete
	}
	for i := 0; i < len(res.Nodes); i += nodeBatch {
		if err := st.AddNodes(id, res.Nodes[i:min(i+nodeBatch, len(res.Nodes))]); err != nil {
			return ScanSummary{}, err
		}
	}
	if err := st.AddUnmeasured(id, res.Unmeasured); err != nil {
		return ScanSummary{}, err
	}
	if err := st.FinishScan(id, status, res.TotalBytes, res.TotalFiles, time.Now().UTC()); err != nil {
		return ScanSummary{}, err
	}
	if scanErr != nil {
		return ScanSummary{}, scanErr
	}
	if status == model.ScanComplete {
		if err := st.PruneScans(keepScans); err != nil {
			return ScanSummary{}, err
		}
	}
	info, err := st.Scan(id)
	if err != nil {
		return ScanSummary{}, err
	}
	return ScanSummary{
		DataVolumeUsed:   dataVolumeUsed(ctx, o.Runner),
		Info:             info,
		TopLevel:         children(res.Nodes, res.Root),
		Largest:          Largest(res.Nodes, 10, 1_000_000_000),
		Unmeasured:       res.Unmeasured,
		HardlinkLimitHit: res.HardlinkLimitHit,
	}, nil
}

// dataVolumeUsed reads the APFS Data volume's used bytes.
func dataVolumeUsed(ctx context.Context, r platform.Runner) model.Bytes {
	unknown := model.Bytes{Source: model.SourceUnknown, Reason: "Data volume size unavailable"}
	if r == nil {
		return unknown
	}
	d, err := probe.CollectDisk(ctx, r)
	if err != nil && len(d.Volumes) == 0 {
		return unknown
	}
	for _, v := range d.Volumes {
		for _, role := range v.Roles {
			if role == "Data" {
				return model.Bytes{Value: v.UsedBytes, Source: model.SourceTool}
			}
		}
	}
	return unknown
}

// children returns the kept direct children of dir, biggest first.
func children(nodes []model.Node, dir string) []model.Node {
	var out []model.Node
	for _, n := range nodes {
		if n.Path != dir && filepath.Dir(n.Path) == dir {
			out = append(out, n)
		}
	}
	sortNodes(out)
	return out
}

// Largest returns up to limit folders (depth ≥ 2) of at least minBytes where
// the space actually is: a node qualifies when no kept child holds half or more of its
// bytes (otherwise the child is the more useful answer). Files ≥ minBytes
// always qualify.
func Largest(nodes []model.Node, limit int, minBytes int64) []model.Node {
	biggestChild := map[string]int64{}
	for _, n := range nodes {
		p := filepath.Dir(n.Path)
		if p != n.Path && n.Bytes > biggestChild[p] {
			biggestChild[p] = n.Bytes
		}
	}
	var out []model.Node
	for _, n := range nodes {
		// The root and top-level folders are already shown in the
		// top-level breakdown; they are never the useful answer here.
		if n.Bytes < minBytes || n.Depth <= 1 {
			continue
		}
		if n.Kind == model.NodeDir && biggestChild[n.Path]*2 >= n.Bytes {
			continue
		}
		out = append(out, n)
	}
	sortNodes(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sortNodes(ns []model.Node) {
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].Bytes != ns[j].Bytes {
			return ns[i].Bytes > ns[j].Bytes
		}
		return ns[i].Path < ns[j].Path
	})
}
