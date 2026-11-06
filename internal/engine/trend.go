package engine

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/store"

	"github.com/sagnik26/macdoc/internal/pathfold"
)

// TrendOptions configures Trend.
type TrendOptions struct {
	Home      string
	StorePath string // "" = store.DefaultPath(Home)
	Days      int    // compare with the newest scan at least this old; 0 = 30
}

// Range is a size or a change that is known only within bounds. A folder
// missing from a scan was below the size scans keep (KeptFloor), so its size
// is between 0 and that floor. Low == High when it is exact.
type Range struct {
	Low  int64 `json:"low"`
	High int64 `json:"high"`
}

// Exact reports whether the range is a single value.
func (r Range) Exact() bool { return r.Low == r.High }

// TrendArea is one well-known place, e.g. Xcode's folders, in both scans.
type TrendArea struct {
	Name   string   `json:"name"`
	Paths  []string `json:"paths"` // the folders summed (display only)
	Before Range    `json:"before"`
	After  Range    `json:"after"`
	Change Range    `json:"change"`
}

// TrendFolder is a folder that grew between the scans.
type TrendFolder struct {
	Path   string `json:"path"`
	Before Range  `json:"before"`
	After  int64  `json:"after"`
	Change Range  `json:"change"`
}

// TrendResult is the data behind `macdoc doctor trend`.
type TrendResult struct {
	From      model.ScanInfo `json:"from"`
	To        model.ScanInfo `json:"to"`
	Days      int            `json:"days"`         // the window asked for
	SpanDays  int            `json:"span_days"`    // the days actually between the scans
	KeptFloor int64          `json:"kept_floor"`   // folders under this size aren't stored
	Total     Range          `json:"total_change"` // all scanned data
	Areas     []TrendArea    `json:"areas"`
	Growing   []TrendFolder  `json:"growing"`
	Note      string         `json:"note,omitempty"`
}

// ErrNoHistory means there is only one complete scan, so nothing to compare.
var ErrNoHistory = errors.New("only one scan so far: trends need two")

// KeptFloor is the scanner's default keep threshold: smaller folders aren't
// stored (scanner.defaultKeepThreshold).
const KeptFloor = 100 << 20

// growMin is the smallest growth listed under "growing".
const growMin = 500_000_000

// growing lists at most this many folders.
const growingMax = 8

// trendAreas are the places compared by name. A "~/" path is under the
// home folder. Areas can overlap (Caches includes Homebrew's cache), so they
// aren't summed.
var trendAreas = []struct {
	name  string
	paths []string
}{
	{"Xcode", []string{"~/Library/Developer/Xcode"}},
	{"iOS simulators", []string{"~/Library/Developer/CoreSimulator", "/Library/Developer/CoreSimulator", "/System/Library/AssetsV2/com_apple_MobileAsset_iOSSimulatorRuntime"}},
	{"Android SDK and emulators", []string{"~/Library/Android", "~/.android"}},
	{"Gradle", []string{"~/.gradle"}},
	{"Node.js caches", []string{"~/.npm", "~/Library/pnpm", "~/Library/Caches/Yarn", "~/.yarn", "~/.bun"}},
	{"CocoaPods", []string{"~/Library/Caches/CocoaPods", "~/.cocoapods"}},
	{"Homebrew", []string{"/opt/homebrew", "~/Library/Caches/Homebrew"}},
	{"Docker", []string{"~/Library/Containers/com.docker.docker", "~/.docker", "~/.orbstack", "~/.colima"}},
	{"Python", []string{"~/Library/Caches/pip", "~/.cache/pip", "~/.cache/uv", "~/.pyenv", "~/.conda", "~/miniconda3", "~/anaconda3"}},
	{"AI models", []string{"~/.ollama", "~/.cache/huggingface", "~/.lmstudio"}},
	{"Rust and Go", []string{"~/.cargo", "~/.rustup", "~/go"}},
	{"App caches", []string{"~/Library/Caches"}},
	{"Downloads", []string{"~/Downloads"}},
	{"Trash", []string{"~/.Trash"}},
	{"iPhone and iPad backups", []string{"~/Library/Application Support/MobileSync"}},
	{"Your files", []string{"~/Desktop", "~/Documents", "~/Pictures", "~/Movies", "~/Music"}},
}

// Trend compares the latest complete scan with the newest one at least Days
// old (or the oldest kept, if none is that old). It reads macdoc's store only.
func Trend(o TrendOptions) (TrendResult, error) {
	if o.Days <= 0 {
		o.Days = 30
	}
	path := o.StorePath
	if path == "" {
		path = store.DefaultPath(o.Home)
	}
	st, err := store.Open(path)
	if err != nil {
		return TrendResult{}, err
	}
	defer func() { _ = st.Close() }()

	res := TrendResult{Days: o.Days, KeptFloor: KeptFloor, Areas: []TrendArea{}, Growing: []TrendFolder{}}
	if res.To, err = st.LatestScan(model.ScanComplete); err != nil {
		return res, err
	}
	res.From, err = st.ScanAtOrBefore(res.To.StartedAt.AddDate(0, 0, -o.Days))
	if errors.Is(err, store.ErrNoScan) {
		res.From, err = st.OldestScan()
	}
	if err != nil {
		return res, err
	}
	if res.From.ID == res.To.ID {
		return res, ErrNoHistory
	}
	res.SpanDays = int(res.To.StartedAt.Sub(res.From.StartedAt).Hours() / 24)
	if res.SpanDays < o.Days {
		res.Note = fmt.Sprintf("macdoc has %s of scans so far, so this covers less than the %d days asked for.", SpanText(res.To.StartedAt.Sub(res.From.StartedAt)), o.Days)
	}

	before, err := nodeSizes(st, res.From.ID)
	if err != nil {
		return res, err
	}
	after, err := nodeSizes(st, res.To.ID)
	if err != nil {
		return res, err
	}
	res.Total = Range{res.To.TotalBytes - res.From.TotalBytes, res.To.TotalBytes - res.From.TotalBytes}

	for _, a := range trendAreas {
		area := TrendArea{Name: a.name}
		for _, p := range a.paths {
			p = expandHome(o.Home, p)
			area.Paths = append(area.Paths, p)
			area.Before = area.Before.add(sizeIn(before, p))
			area.After = area.After.add(sizeIn(after, p))
		}
		if area.Before.High <= int64(len(a.paths))*KeptFloor && area.Before.Low == 0 &&
			area.After.High <= int64(len(a.paths))*KeptFloor && area.After.Low == 0 {
			continue // never big enough to be stored
		}
		area.Change = Range{area.After.Low - area.Before.High, area.After.High - area.Before.Low}
		res.Areas = append(res.Areas, area)
	}
	sort.SliceStable(res.Areas, func(i, j int) bool { return mid(res.Areas[i].Change) > mid(res.Areas[j].Change) })

	res.Growing = Growing(before, after)
	return res, nil
}

// nodeSizes maps each stored node's folded path to its node.
func nodeSizes(st *store.Store, scanID string) (map[string]model.Node, error) {
	nodes, err := st.Nodes(scanID, store.NodeQuery{})
	if err != nil {
		return nil, err
	}
	m := make(map[string]model.Node, len(nodes))
	for _, n := range nodes {
		m[fold(n.Path)] = n
	}
	return m, nil
}

// sizeIn is p's size in a scan: exact if stored, else 0 up to KeptFloor.
func sizeIn(nodes map[string]model.Node, p string) Range { return sizeInKey(nodes, fold(p)) }

// sizeInKey is sizeIn for a path that is already folded.
func sizeInKey(nodes map[string]model.Node, key string) Range {
	if n, ok := nodes[key]; ok {
		return Range{n.Bytes, n.Bytes}
	}
	return Range{0, KeptFloor}
}

// fold is the key under which a path is compared and looked up.
func fold(p string) string { return pathfold.Fold(p) }

func (r Range) add(o Range) Range { return Range{r.Low + o.Low, r.High + o.High} }

func mid(r Range) int64 { return r.Low + (r.High-r.Low)/2 }

// Growing finds the folders that grew by at least growMin, keeping the
// deepest one that explains most of a growth: a folder is dropped when one
// of its descendants grew by at least 80% as much. The maps are keyed by
// folded path, as nodeSizes builds them. It is exported so the tests can
// compare it with a reference and benchmark it.
//
// Folders are judged deepest first, so everything below a folder has been
// decided before the folder is. Each folder passes to its nearest candidate
// ancestor what the ancestor needs to know, so each decision is constant
// time once that ancestor is found by one walk up the path.
func Growing(before, after map[string]model.Node) []TrendFolder {
	var cands []growCand
	for key, n := range after {
		if n.Kind != model.NodeDir {
			continue
		}
		b := sizeInKey(before, key)
		ch := Range{n.Bytes - b.High, n.Bytes - b.Low}
		if ch.Low < growMin {
			continue
		}
		// Space that moved rather than grew (an app update replacing its
		// Versions/155 with Versions/156) shows as growth of the new folder
		// only: its parent didn't grow.
		if p, ok := after[fold(filepath.Dir(n.Path))]; ok && p.Path != n.Path {
			pb := sizeIn(before, p.Path)
			if (p.Bytes-pb.High)*2 < ch.Low {
				continue
			}
		}
		cands = append(cands, growCand{TrendFolder{Path: n.Path, Before: b, After: n.Bytes, Change: ch}, key, strings.Count(n.Path, "/")})
	}
	// Deepest first, so descendants are judged before their parents.
	slices.SortFunc(cands, func(a, b growCand) int {
		if a.depth != b.depth {
			return cmp.Compare(b.depth, a.depth)
		}
		return strings.Compare(a.Path, b.Path)
	})
	at := make(map[string]int, len(cands)) // folded path → index in cands
	for i, c := range cands {
		if c.key != "/" { // "/" is below nothing, so it is never an ancestor to find
			at[c.key] = i
		}
	}
	// For each folder, over the kept folders below it: the largest growth,
	// and the summed growth of those with no other kept folder between them
	// and it.
	maxBelow := make([]int64, len(cands))
	sumBelow := make([]int64, len(cands))
	kept := make([]bool, len(cands))
	out := []TrendFolder{}
	for i, c := range cands {
		low := c.Change.Low
		biggest, summed := maxBelow[i], sumBelow[i]
		if c.key == "/" {
			biggest, summed = rootBelow(cands[:i], kept, at)
		}
		kept[i] = biggest*5 < low*4
		// A parent whose kept descendants explain all but growMin of its
		// growth adds nothing.
		if rest := low - summed; kept[i] && (rest >= growMin || rest >= low) {
			out = append(out, c.TrendFolder)
		}
		if c.key == "/" {
			continue
		}
		if up := nearest(c.key, at); up >= 0 {
			if kept[i] {
				summed = low
				biggest = max(biggest, low)
			}
			maxBelow[up] = max(maxBelow[up], biggest)
			sumBelow[up] += summed
		}
	}
	slices.SortStableFunc(out, func(a, b TrendFolder) int { return cmp.Compare(b.Change.Low, a.Change.Low) })
	if len(out) > growingMax {
		out = out[:growingMax]
	}
	return out
}

// growCand is a folder that grew enough to be listed, before it is judged.
type growCand struct {
	TrendFolder
	key   string // folded path
	depth int
}

// nearest is the index of the closest folder above key that is in at, or -1.
func nearest(key string, at map[string]int) int {
	for i := strings.LastIndexByte(key, '/'); i >= 0; i = strings.LastIndexByte(key[:i], '/') {
		if j, ok := at[key[:i]]; ok {
			return j
		}
	}
	return -1
}

// rootBelow is maxBelow and sumBelow for the folder "/". It looks only at
// the folders judged before it (those at least two levels down), and a kept
// folder counts in the sum only when no kept folder above it is among them.
// Every other path counts as below "/". These are the original rules, kept
// as they were.
func rootBelow(before []growCand, kept []bool, at map[string]int) (biggest, summed int64) {
	for j, c := range before {
		if !kept[j] {
			continue
		}
		biggest = max(biggest, c.Change.Low)
		covered := false // some kept folder between c and "/"
		for i := strings.LastIndexByte(c.key, '/'); i >= 0 && !covered; i = strings.LastIndexByte(c.key[:i], '/') {
			k, ok := at[c.key[:i]]
			covered = ok && kept[k]
		}
		if !covered {
			summed += c.Change.Low
		}
	}
	return biggest, summed
}

func expandHome(home, p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return p
}

// SpanText is a duration in days or hours.
func SpanText(d time.Duration) string {
	switch days := int(d.Hours() / 24); {
	case days >= 2:
		return fmt.Sprintf("%d days", days)
	case days == 1:
		return "1 day"
	}
	h := int(d.Hours())
	if h == 1 {
		return "1 hour"
	}
	return fmt.Sprintf("%d hours", max(h, 0))
}
