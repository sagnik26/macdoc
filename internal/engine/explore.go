package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/pathfold"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
)

// exploreLimit is how many entries a folder lists by default.
const exploreLimit = 100

// largestFilesLimit is how many of the biggest files below a folder are listed.
const largestFilesLimit = 10

// ExploreOptions says which folder of the latest scan to open.
type ExploreOptions struct {
	Home      string
	StorePath string    // default store.DefaultPath(Home)
	Path      string    // the folder to open; default Home; "~" and "~/x" are expanded
	Limit     int       // entries to list; default 100
	Now       time.Time // default time.Now()
}

// ExploreEntry is one item inside the folder being explored.
type ExploreEntry struct {
	Name  string  `json:"name"`
	Path  string  `json:"path,omitempty"` // empty for the "smaller items" remainder
	Kind  string  `json:"kind"`           // "dir", "file", or "other" for the remainder
	Bytes int64   `json:"bytes"`          // allocated bytes, measured by the scan
	Share float64 `json:"share"`          // of the folder's bytes, 0 to 1
}

// ExploreResult is the data behind `macdoc doctor explore` and the dashboard's
// explorer. It is read from the latest scan only; nothing is measured again.
type ExploreResult struct {
	Scan    model.ScanInfo `json:"scan"`
	ScanAge string         `json:"scan_age"`
	Path    string         `json:"path"`
	Parent  string         `json:"parent,omitempty"` // empty at the top
	// Bytes is the folder's total. It is absent when Status is "unavailable":
	// an unmeasured folder is not an empty one.
	Bytes *int64 `json:"bytes,omitempty"`
	// Status is "complete", "partial" (some paths below could not be read, so
	// sizes are lower bounds) or "unavailable" (the scan kept no detail here).
	Status       string         `json:"status"`
	Note         string         `json:"note,omitempty"`
	Entries      []ExploreEntry `json:"entries"`
	LargestFiles []ExploreEntry `json:"largest_files"`
}

// Explore lists what the latest scan knows about one folder: its biggest
// items, with how much of the folder each holds, and the biggest files below
// it. It is read-only and takes a path only to look at, never as a target.
func Explore(ctx context.Context, o ExploreOptions) (ExploreResult, error) {
	if o.Home == "" {
		return ExploreResult{}, errors.New("explore: home directory is required")
	}
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Limit <= 0 {
		o.Limit = exploreLimit
	}
	dir, err := explorePath(o.Home, o.Path)
	if err != nil {
		return ExploreResult{}, err
	}

	st, err := store.Open(o.StorePath)
	if err != nil {
		return ExploreResult{}, err
	}
	defer func() { _ = st.Close() }()
	info, err := st.LatestScan(model.ScanComplete)
	if errors.Is(err, store.ErrNoScan) {
		return ExploreResult{}, ErrNoScan
	}
	if err != nil {
		return ExploreResult{}, err
	}
	nodes, err := st.Nodes(info.ID, store.Query{Prefix: dir})
	if err != nil {
		return ExploreResult{}, err
	}
	unmeasured, err := st.Unmeasured(info.ID)
	if err != nil {
		return ExploreResult{}, err
	}

	res := ExploreResult{Scan: info, ScanAge: FormatAge(o.Now.Sub(scanTime(info))), Path: dir,
		Entries: []ExploreEntry{}, LargestFiles: []ExploreEntry{}}
	if p := filepath.Dir(dir); p != dir {
		res.Parent = p
	}
	folder, ok := folderNode(nodes, dir)
	if !ok {
		res.Status = "unavailable"
		res.Note = "the scan kept no detail for this folder: it is smaller than the scan's keep threshold, or it is outside the scan"
		return res, nil
	}
	res.Bytes = &folder.Bytes
	res.Status, res.Note = exploreStatus(dir, unmeasured)
	res.Entries = exploreEntries(nodes, folder, o.Limit)
	res.LargestFiles = largestFiles(nodes, folder)
	return res, nil
}

// explorePath resolves the folder to open: home by default, "~" expanded, and
// otherwise an absolute, clean path.
func explorePath(home, p string) (string, error) {
	if p == "" {
		return filepath.Clean(home), nil
	}
	p = recipes.ExpandHome(home, p)
	if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
		return "", errors.New("explore: the path must be absolute or start with ~")
	}
	return filepath.Clean(p), nil
}

// folderNode finds the node for dir among nodes (names compare without regard
// to case, as the store does).
func folderNode(nodes []model.Node, dir string) (model.Node, bool) {
	want := pathfold.Fold(dir)
	for _, n := range nodes {
		if pathfold.Fold(n.Path) == want && n.Kind == model.NodeDir {
			return n, true
		}
	}
	return model.Node{}, false
}

// exploreStatus is "partial" when the scan could not read some path at or
// below dir, since the sizes there are then lower bounds.
func exploreStatus(dir string, unmeasured []model.Unmeasured) (status, note string) {
	prefix := pathfold.Fold(dir)
	n := 0
	for _, u := range unmeasured {
		if p := pathfold.Fold(u.Path); p == prefix || strings.HasPrefix(p, strings.TrimSuffix(prefix, "/")+"/") {
			n++
		}
	}
	if n == 0 {
		return "complete", ""
	}
	word := "paths"
	if n == 1 {
		word = "path"
	}
	return "partial", fmt.Sprintf("%d %s below could not be read (grant Full Disk Access and rescan), so sizes here are lower bounds", n, word)
}

// exploreEntries lists the kept direct children of folder, biggest first, and
// a "smaller items" remainder so the entries add up to the folder.
func exploreEntries(nodes []model.Node, folder model.Node, limit int) []ExploreEntry {
	parent := pathfold.Fold(folder.Path)
	var kids []model.Node
	var sum int64
	for _, n := range nodes {
		if pathfold.Fold(filepath.Dir(n.Path)) == parent && pathfold.Fold(n.Path) != parent {
			kids = append(kids, n)
			sum += n.Bytes
		}
	}
	sort.Slice(kids, func(i, j int) bool {
		if kids[i].Bytes != kids[j].Bytes {
			return kids[i].Bytes > kids[j].Bytes
		}
		return kids[i].Path < kids[j].Path
	})
	out := make([]ExploreEntry, 0, len(kids)+1)
	for _, n := range kids {
		out = append(out, entryOf(n, folder.Bytes))
	}
	if rest := folder.Bytes - sum; rest > 0 {
		out = append(out, ExploreEntry{Name: "smaller items", Kind: "other", Bytes: rest, Share: share(rest, folder.Bytes)})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// largestFiles lists the biggest kept files at any depth below folder.
func largestFiles(nodes []model.Node, folder model.Node) []ExploreEntry {
	var files []model.Node
	for _, n := range nodes {
		if n.Kind == model.NodeFile {
			files = append(files, n)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Bytes != files[j].Bytes {
			return files[i].Bytes > files[j].Bytes
		}
		return files[i].Path < files[j].Path
	})
	if len(files) > largestFilesLimit {
		files = files[:largestFilesLimit]
	}
	out := make([]ExploreEntry, 0, len(files))
	for _, n := range files {
		out = append(out, entryOf(n, folder.Bytes))
	}
	return out
}

func entryOf(n model.Node, total int64) ExploreEntry {
	return ExploreEntry{Name: filepath.Base(n.Path), Path: n.Path, Kind: string(n.Kind), Bytes: n.Bytes, Share: share(n.Bytes, total)}
}

func share(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}
