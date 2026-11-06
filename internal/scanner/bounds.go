package scanner

import (
	"context"
	"math"
	"sync"
)

// CloneLimit is the most clone groups, and the most partly seen hard-linked
// inodes, that Measure keeps. A group costs about 60 bytes, so the cap holds
// the walk near 15 MB each. Past it the upper bound is reported unknown
// rather than guessed.
const CloneLimit = 250_000

// Bounds is what removing one folder frees, as a range
// (docs/adr/0005-size-by-allocated-blocks.md, stage 3).
//
// Lower never overstates: the folders' own blocks plus the files' private
// sizes, a file with several links counting 0. Upper never understates when
// Bounded: it counts each clone group once if every member lies inside the
// folder, and nothing but private sizes for a group with a member outside;
// it is also capped at the total less half of the shared space, which holds
// for any pattern of sharing. The true freed amount lies between them, apart
// from space that snapshots or open files still hold.
type Bounds struct {
	Lower int64
	Upper int64 // meaningful only if Bounded
	// Bounded is false when Upper could not be worked out: more clone groups
	// than the limit, or folders that could not be read. Then only Lower
	// holds, and Upper is not zero but unknown.
	Bounded bool
	// Lacking is the number of files the kernel gave no private size for.
	// Lower leaves them out (so it stays a lower bound) and Upper counts their
	// full allocation.
	Lacking int64
	// Unread is the number of folders that could not be read.
	Unread int
	Files  int64 // files and other non-folder entries counted
}

// group is the members of one clone ID seen so far.
type group struct {
	seen, refs uint32 // members inside the folder; members everywhere
	sum, most  int64  // their allocations added up; the largest one
	priv       int64  // their private sizes added up
}

// tracker collects, during a walk, what Bounds needs from each counted file.
// A nil tracker ignores everything, so a full scan carries none.
type tracker struct {
	mu     sync.Mutex
	limit  int
	groups map[uint64]group
	over   bool  // the group map would have passed limit
	alloc  int64 // allocation of every counted entry that is not a folder
	freed  int64 // their private sizes, where known
	shared int64 // allocation less private size, over single-link files
	lack   int64
	files  int64
}

func newTracker(limit int) *tracker {
	return &tracker{limit: limit, groups: make(map[uint64]group)}
}

// note records one counted entry: a file counted once even if it has many
// links, or another kind of entry that holds its own blocks.
func (t *tracker) note(e *entry) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.files++
	t.alloc += e.bytes
	v, ok := e.freed()
	if !ok {
		t.lack++
		return
	}
	v = min(max(v, 0), e.bytes)
	t.freed += v
	if e.nlink > 1 {
		return // a hard link: its inode is counted once, and it shares nothing else known
	}
	t.shared += e.bytes - min(max(e.private, 0), e.bytes)
	if e.refs > 1 {
		t.join(e)
	}
}

// join adds a clone-group member. Called with t.mu held.
func (t *tracker) join(e *entry) {
	g, ok := t.groups[e.clone]
	if !ok {
		if len(t.groups) >= t.limit {
			t.over = true
			return
		}
		g.refs = e.refs
	}
	g.seen++
	g.sum += e.bytes
	g.most = max(g.most, e.bytes)
	g.priv += min(max(e.private, 0), e.bytes)
	t.groups[e.clone] = g
}

// bounds turns what was collected into a range. total is the walk's allocated
// bytes, hard links counted once.
func (t *tracker) bounds(total int64) Bounds {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := Bounds{Lower: total - t.alloc + t.freed, Lacking: t.lack, Files: t.files}
	if t.over {
		return b
	}
	// Every block that frees with two or more holders in the folder shows up
	// in the shared part of at least two files.
	upper := total - t.shared/2
	// A clone group whose members are all inside holds one copy of its data;
	// with a member outside it holds only what its members keep to themselves.
	alt := total
	for _, g := range t.groups {
		keep := g.priv
		if g.seen >= g.refs {
			keep = max(g.most, g.priv)
		}
		alt -= g.sum - keep
	}
	b.Upper = max(min(upper, alt), b.Lower)
	b.Bounded = true
	return b
}

// Measure walks the folder at path once and returns the range of space that
// removing it frees. It reads, never changes: it follows no symlink, does not
// cross onto another device, and holds at most limit clone groups (CloneLimit
// if limit is not positive). It reads attributes the full scan does not, so
// it costs more per file, and is meant for the one folder being explained.
//
// path must come from a scan's own record of a suggestion's target; Measure
// is not a way to ask about a path a person typed. It returns ctx's error if
// ctx ends first.
func Measure(ctx context.Context, path string, limit int) (Bounds, error) {
	if limit <= 0 {
		limit = CloneLimit
	}
	t := newTracker(limit)
	res, err := walk(ctx, Options{
		Root:          path,
		KeepThreshold: math.MaxInt64, // keep only the root, so no node list grows
		HardlinkLimit: limit,
	}, t)
	if err != nil {
		return Bounds{}, err
	}
	if res.Incomplete {
		if err := ctx.Err(); err != nil {
			return Bounds{}, err
		}
		return Bounds{}, context.Canceled
	}
	b := t.bounds(res.TotalBytes)
	b.Unread = len(res.Unmeasured)
	if b.Unread > 0 {
		b.Bounded = false // what could not be read is missing from Upper
	}
	return b, nil
}
