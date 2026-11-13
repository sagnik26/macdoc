package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/inventory"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
)

// softwareDepth is how far below home the candidate folders are: Library
// (1), Caches (2), and one program folder (3). See inventory.Attribute.
const softwareDepth = 3

// SoftwareOptions controls one software inventory.
type SoftwareOptions struct {
	Home      string
	StorePath string // default store.DefaultPath(Home)
	Runner    platform.Runner
	AppDirs   []string // default /Applications and Home/Applications
	BinDirs   []string // default: the folders on PATH
	JavaDirs  []string // default: the system and per-user Java folders
	HasTool   func(name string) bool
	Catalog   *recipes.Catalog // default recipes.Load()
	Now       time.Time        // default time.Now()
}

// OwnedFolder is one scanned folder that belongs to a piece of software.
type OwnedFolder struct {
	Path       string  `json:"path"`
	Bytes      int64   `json:"bytes"`      // allocated bytes, measured by the scan
	Confidence float64 `json:"confidence"` // 0 to 1, see inventory.Attribute
	Evidence   string  `json:"evidence"`
}

// SoftwareEntry is one installed program with the data folders found for it.
// Bytes is the sum of Folders, measured by the scan; zero means no data
// folder was found, not that the size is unknown.
type SoftwareEntry struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	Version   string          `json:"version,omitempty"`
	BundleID  string          `json:"bundle_id,omitempty"`
	Source    string          `json:"source"`
	Installed *time.Time      `json:"installed,omitempty"` // absent when unknown
	Bytes     int64           `json:"bytes"`
	Folders   []OwnedFolder   `json:"folders"`
	Slim      *inventory.Slim `json:"slim,omitempty"` // advice only; absent when the executable could not be read
	// CoveredBy lists the recipes whose match names this software: the
	// cleanups macdoc can offer for it. Empty means advisory only (ADR 0007).
	CoveredBy []string `json:"covered_by"`
}

// SourceState says how one discovery source did in this scan.
type SourceState struct {
	Source string `json:"source"`
	State  string `json:"state"` // "ok", "unknown" or "absent"
	Reason string `json:"reason,omitempty"`
}

// SoftwareResult is the data behind `macdoc doctor software`.
type SoftwareResult struct {
	Scan         model.ScanInfo  `json:"scan"`
	ScanAge      string          `json:"scan_age"`
	Software     []SoftwareEntry `json:"software"`
	Unattributed []OwnedFolder   `json:"unattributed"` // folders no software explains, largest first
	// New lists the IDs of software that wasn't in the previous scan's
	// snapshot. It is empty when there is no earlier snapshot to compare with.
	New      []string      `json:"new"`
	Sources  []SourceState `json:"sources"`
	Warnings []string      `json:"warnings,omitempty"`
}

// SoftwareSummary is the last stored snapshot in brief, for the overview and
// the dashboard. It is read from the store only: no discovery runs.
type SoftwareSummary struct {
	ScanID     string   `json:"scan_id"`
	Count      int      `json:"count"`
	Programs   int      `json:"programs"`    // the part of Count that are programs on PATH
	Headline   string   `json:"headline"`    // the counts by category, for display
	OwnedBytes int64    `json:"owned_bytes"` // measured by the scan, in the data folders of the software
	NewNames   []string `json:"new_names"`   // names of software new since the scan before this one
}

// Software finds what is installed on this Mac, attributes the latest scan's
// folders to it, and stores the snapshot with that scan. It never removes or
// changes anything on the disk (ADR 0007). It needs a complete scan.
func Software(ctx context.Context, o SoftwareOptions) (SoftwareResult, error) {
	if o.Home == "" {
		return SoftwareResult{}, errors.New("software: home directory is required")
	}
	o, err := o.withDefaults()
	if err != nil {
		return SoftwareResult{}, err
	}

	st, err := store.Open(o.StorePath)
	if err != nil {
		return SoftwareResult{}, err
	}
	defer func() { _ = st.Close() }()
	info, err := st.LatestScan(model.ScanComplete)
	if errors.Is(err, store.ErrNoScan) {
		return SoftwareResult{}, ErrNoScan
	}
	if err != nil {
		return SoftwareResult{}, err
	}

	nodes, err := st.Nodes(info.ID, store.Query{Prefix: o.Home, MaxDepth: softwareDepth})
	if err != nil {
		return SoftwareResult{}, err
	}
	folders := make([]inventory.Folder, 0, len(nodes))
	for _, n := range nodes {
		if n.Path == o.Home {
			continue
		}
		folders = append(folders, inventory.Folder{Path: n.Path, Bytes: n.Bytes})
	}

	disc := inventory.Discover(ctx, inventory.Env{
		Home: o.Home, AppDirs: o.AppDirs, BinDirs: o.BinDirs, JavaDirs: o.JavaDirs, Runner: o.Runner, HasTool: o.HasTool, Now: o.Now,
	})
	disc.Software = dropDuplicates(disc.Software)
	owners, _ := inventory.Attribute(o.Home, disc.Software, folders)

	if err := st.SaveInventory(info.ID, snapshotOf(disc, owners)); err != nil {
		return SoftwareResult{}, err
	}

	res := SoftwareResult{Scan: info, ScanAge: FormatAge(o.Now.Sub(scanTime(info)))}
	res.Software = entries(disc.Software, owners, coverIndex(o.Catalog))
	res.Unattributed = unexplained(folders, owners)
	for _, s := range disc.Statuses {
		res.Sources = append(res.Sources, SourceState{Source: s.Source, State: s.State, Reason: s.Reason})
	}
	res.New, res.Warnings, err = newSince(st, info.ID, pendingOf(disc.Software))
	if err != nil {
		return SoftwareResult{}, err
	}
	if res.New == nil {
		res.New = []string{} // JSON shows [], never null
	}
	return res, nil
}

// SoftwareSnapshot summarizes the snapshot stored for scanID. It returns nil
// when that scan has none (taken before this feature, or never inventoried).
// It only reads the store.
func SoftwareSnapshot(storePath, scanID string) (*SoftwareSummary, error) {
	st, err := store.Open(storePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	inv, err := st.Inventory(scanID)
	if err != nil {
		return nil, err
	}
	if len(inv.Sources) == 0 {
		return nil, nil
	}
	sum := &SoftwareSummary{ScanID: scanID, Count: len(inv.Software), NewNames: []string{}}
	for _, r := range inv.Software {
		if r.Source == "tools" {
			sum.Programs++
		}
	}
	bytesOf := map[string]int64{}
	for _, o := range inv.Owners {
		sum.OwnedBytes += o.Bytes
		bytesOf[o.SoftwareID] += o.Bytes
	}
	var entries []SoftwareEntry
	for _, r := range inv.Software {
		entries = append(entries, SoftwareEntry{ID: r.ID, Kind: r.Kind, Bytes: bytesOf[r.ID]})
	}
	sum.Headline = CategoryHeadline(entries)
	names := map[string]string{}
	var cur []pending
	for _, r := range inv.Software {
		cur = append(cur, pending{ID: r.ID, Source: r.Source})
		names[r.ID] = r.Name
	}
	newIDs, _, err := newSince(st, scanID, cur)
	if err != nil {
		return nil, err
	}
	for _, id := range newIDs {
		sum.NewNames = append(sum.NewNames, names[id])
	}
	sort.Strings(sum.NewNames)
	return sum, nil
}

// withDefaults fills in the options a caller leaves out. The catalog is the
// only default that can fail.
func (o SoftwareOptions) withDefaults() (SoftwareOptions, error) {
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Runner == nil {
		o.Runner = platform.Exec{}
	}
	if o.HasTool == nil {
		o.HasTool = func(name string) bool { _, err := platform.ResolveTool(name); return err == nil }
	}
	if o.AppDirs == nil {
		o.AppDirs = []string{"/Applications", filepath.Join(o.Home, "Applications")}
	}
	if o.BinDirs == nil {
		o.BinDirs = filepath.SplitList(os.Getenv("PATH"))
	}
	if o.JavaDirs == nil {
		o.JavaDirs = []string{"/Library/Java/JavaVirtualMachines", filepath.Join(o.Home, "Library/Java/JavaVirtualMachines")}
	}
	if o.Catalog == nil {
		cat, err := recipes.Load()
		if err != nil {
			return o, err
		}
		o.Catalog = cat
	}
	return o, nil
}

// dropDuplicates removes two kinds of double count. A PATH program that a
// package manager already lists is dropped, and so is an App Store entry whose
// app is already installed as a bundle.
func dropDuplicates(sw []inventory.Software) []inventory.Software {
	managed := map[string]bool{}
	apps := map[string]bool{}
	for _, s := range sw {
		if slices.Contains(inventory.PackageSources, s.Source) {
			managed[s.Name] = true
		}
		if s.Source == "apps" {
			apps[s.Name] = true
		}
	}
	out := sw[:0:0]
	for _, s := range sw {
		if s.Source == "tools" && managed[s.Name] {
			continue
		}
		if s.Source == "mas" && apps[s.Name] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// coverIndex maps each match entry (such as "npm:yarn") to the IDs of the
// recipes that name it.
func coverIndex(cat *recipes.Catalog) map[string][]string {
	idx := map[string][]string{}
	for _, r := range cat.Recipes {
		for _, m := range r.Match {
			idx[m] = append(idx[m], r.ID)
		}
	}
	for k := range idx {
		sort.Strings(idx[k])
	}
	return idx
}

// softwareKey turns a discovered software ID into the form recipe match
// entries use. "app:<bundle id>" is "bundle:<bundle id>"; "app-name:<name>" is
// "app:<name>"; brew and npm IDs already read the same way.
func softwareKey(id string) string {
	switch {
	case strings.HasPrefix(id, "app:"):
		return "bundle:" + strings.TrimPrefix(id, "app:")
	case strings.HasPrefix(id, "app-name:"):
		return "app:" + strings.TrimPrefix(id, "app-name:")
	}
	return id
}

// snapshotOf converts discovery output to the store's rows.
func snapshotOf(disc inventory.Result, owners []inventory.Owner) store.Inventory {
	var inv store.Inventory
	for _, s := range disc.Software {
		inv.Software = append(inv.Software, store.SoftwareRow{ID: s.ID, Name: s.Name, Kind: s.Kind,
			Version: s.Version, BundleID: s.BundleID, Source: s.Source, Installed: s.Installed})
	}
	for _, o := range owners {
		inv.Owners = append(inv.Owners, store.OwnerRow{SoftwareID: o.SoftwareID, Path: o.Path,
			Bytes: o.Bytes, Confidence: o.Confidence, Evidence: o.Evidence})
	}
	for _, s := range disc.Statuses {
		inv.Sources = append(inv.Sources, store.SourceRow{Source: s.Source, State: s.State, Reason: s.Reason})
	}
	return inv
}

// entries groups owned folders under their software and adds the recipes
// that cover each one. Software with no folder is still listed, with zero
// bytes.
func entries(sw []inventory.Software, owners []inventory.Owner, cover map[string][]string) []SoftwareEntry {
	byID := map[string][]OwnedFolder{}
	for _, o := range owners {
		byID[o.SoftwareID] = append(byID[o.SoftwareID], OwnedFolder{Path: o.Path, Bytes: o.Bytes,
			Confidence: o.Confidence, Evidence: o.Evidence})
	}
	out := make([]SoftwareEntry, 0, len(sw))
	for _, s := range sw {
		e := SoftwareEntry{ID: s.ID, Name: s.Name, Kind: s.Kind, Version: s.Version, BundleID: s.BundleID, Source: s.Source, Slim: s.Slim}
		if !s.Installed.IsZero() {
			t := s.Installed
			e.Installed = &t
		}
		e.Folders = byID[s.ID]
		if e.Folders == nil {
			e.Folders = []OwnedFolder{} // JSON shows [], never null
		}
		for _, f := range e.Folders {
			e.Bytes += f.Bytes
		}
		e.CoveredBy = append([]string{}, cover[softwareKey(s.ID)]...)
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// unexplained lists the largest kept folders under home that no software owns,
// wherever they are, not only in the places discovery knows about. A folder
// inside another listed folder (owned or not) is left out, so each byte is
// counted once.
func unexplained(folders []inventory.Folder, owners []inventory.Owner) []OwnedFolder {
	owned := map[string]bool{}
	for _, o := range owners {
		owned[o.Path] = true
	}
	sorted := append([]inventory.Folder{}, folders...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Bytes != sorted[j].Bytes {
			return sorted[i].Bytes > sorted[j].Bytes
		}
		return sorted[i].Path < sorted[j].Path
	})
	var out []OwnedFolder
	var listed []string
	ownedList := ownedPaths(owned)
	for _, f := range sorted {
		if owned[f.Path] || insideAny(f.Path, listed) || insideAny(f.Path, ownedList) || containsAny(f.Path, ownedList) {
			continue
		}
		listed = append(listed, f.Path)
		out = append(out, OwnedFolder{Path: f.Path, Bytes: f.Bytes})
	}
	return out
}

// containsAny reports whether dir holds one of the folders in paths. Such a
// folder is not listed whole: its unowned parts show up through its children.
func containsAny(dir string, paths []string) bool {
	for _, p := range paths {
		if strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}

// insideAny reports whether path lies inside one of the folders in dirs.
func insideAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(path, d+"/") {
			return true
		}
	}
	return false
}

// ownedPaths lists the keys of an owned-path set.
func ownedPaths(owned map[string]bool) []string {
	out := make([]string, 0, len(owned))
	for p := range owned {
		out = append(out, p)
	}
	return out
}

// newSince lists the IDs in ids that the previous scan's snapshot doesn't
// have. A previous scan without a snapshot (taken before this feature) gives
// no comparison, and everything would look new, so it returns a warning
// instead.
func newSince(st *store.Store, scanID string, cur []pending) ([]string, []string, error) {
	prev, ok, err := st.PreviousScanID(scanID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, nil
	}
	old, err := st.Inventory(prev)
	if err != nil {
		return nil, nil, err
	}
	if len(old.Sources) == 0 {
		return nil, []string{"the previous scan has no software snapshot, so nothing can be called new yet"}, nil
	}
	seen := map[string]bool{}
	for _, r := range old.Software {
		seen[r.ID] = true
	}
	// A source that didn't run OK last time can't show what was there, so its
	// programs are not called new; they're just not compared.
	ranOK := map[string]bool{}
	for _, src := range old.Sources {
		ranOK[src.Source] = src.State == "ok"
	}
	var out []string
	for _, c := range cur {
		if !seen[c.ID] && ranOK[c.Source] {
			out = append(out, c.ID)
		}
	}
	sort.Strings(out)
	return out, nil, nil
}

// CoveredBy returns the IDs of the recipes whose match names the software with
// this ID, as discovery reports it (for example "npm:yarn" or
// "app:com.docker.docker").
func CoveredBy(cat *recipes.Catalog, softwareID string) []string {
	return coverIndex(cat)[softwareKey(softwareID)]
}

// CategoryHeadline counts the entries by category, in CategoryOrder, and gives
// the bytes in their data folders. Empty categories are left out.
func CategoryHeadline(sw []SoftwareEntry) string {
	counts := map[string]int{}
	var owned int64
	for _, e := range sw {
		counts[inventory.Category(e.Kind)]++
		owned += e.Bytes
	}
	var parts []string
	for _, c := range inventory.CategoryOrder {
		if n := counts[c]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, c))
		}
	}
	if len(parts) == 0 {
		parts = []string{"nothing found"}
	}
	return strings.Join(parts, " · ") + " · " + DecimalBytes(owned) + " in their data folders"
}

// pending is one program found in this scan: its ID and the source that found it.
type pending struct {
	ID     string
	Source string
}

// pendingOf lists the programs of a discovery run.
func pendingOf(sw []inventory.Software) []pending {
	out := make([]pending, 0, len(sw))
	for _, s := range sw {
		out = append(out, pending{ID: s.ID, Source: s.Source})
	}
	return out
}
