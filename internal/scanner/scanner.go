// Package scanner walks the Data volume and measures allocated space with
// bounded memory, at background priority. It is strictly read-only.
//
// Sizes are allocated blocks (st_blocks * 512), which over-states the space
// freed by deleting a file that shares blocks with a clone
// (docs/adr/0005-size-by-allocated-blocks.md). Each kept node also carries a
// private size, a lower bound of what deleting it frees: the sum of its
// files' ATTR_CMNEXT_PRIVATESIZE plus its folders' own blocks. Only the
// getattrlistbulk reader can read it. When any file below a node was
// measured without one (the fstatat reader, or a volume that does not report
// it) the node's private size is nil, meaning unknown; it is never zero.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/pathfold"
)

// DataVolume is the default scan root on macOS.
const DataVolume = "/System/Volumes/Data"

// Options configures a scan.
type Options struct {
	// Root is the directory to scan (default DataVolume). The scan never
	// crosses onto another device (st_dev) and never follows symlinks.
	Root string
	// MapPrefix, if non-empty, is stripped from every reported path and
	// replaced by "/" (firmlink mapping: with Root=/System/Volumes/Data and
	// MapPrefix=/System/Volumes/Data, ".../Data/Users/x" is reported as
	// "/Users/x"). Default: equal to Root when Root == DataVolume, else "".
	MapPrefix string
	// Skip lists absolute (unmapped) paths that are not descended or counted
	// at all (e.g. .Spotlight-V100, .fseventsd, the quarantine root).
	Skip []string
	// Opaque lists absolute (unmapped) paths that are fully measured but
	// whose descendants are never kept (e.g. /private/var/folders).
	Opaque []string
	// KeepThreshold keeps any directory or file with at least this many
	// allocated bytes (default 100 MB).
	KeepThreshold int64
	// Keep, if non-nil, additionally keeps any directory for which it
	// returns true (mapped path), regardless of size, e.g. recipe roots and
	// project roots. It must be cheap and must not touch the filesystem.
	Keep func(path string) bool
	// Workers is the number of concurrent directory readers (default 4,
	// clamped to 1..16).
	Workers int
	// HardlinkLimit caps the number of (dev, ino) pairs tracked for
	// hard-link de-duplication (default 1,000,000). Beyond it, further hard
	// links are counted every time and Result.HardlinkLimitHit is set.
	HardlinkLimit int
	// Progress, if non-nil, is called at most every ProgressEvery.
	Progress      func(Progress)
	ProgressEvery time.Duration // default 250ms
}

// Progress is a periodic scan update.
type Progress struct {
	Files      int64
	Bytes      int64
	CurrentDir string // mapped path
	Elapsed    time.Duration
}

// Result is the outcome of a scan. Nodes are sorted by Bytes descending.
type Result struct {
	Root             string             // mapped root, e.g. "/"
	TotalBytes       int64              // allocated bytes under Root (excl. Skip)
	TotalFiles       int64              //
	Nodes            []model.Node       // kept nodes (always includes the root)
	Unmeasured       []model.Unmeasured // unreadable paths, mapped
	HardlinkLimitHit bool               //
	Incomplete       bool               // ctx was canceled; totals are partial
	Duration         time.Duration      //
}

const (
	defaultKeepThreshold = 100 << 20
	defaultWorkers       = 4
	widest               = 16
	defaultHardlinkLimit = 1_000_000
	defaultProgressEvery = 250 * time.Millisecond

	// batchSize is how many names are read from a directory at a time, so a
	// directory with millions of entries never sits in memory at once and
	// cancellation is noticed between batches.
	batchSize = 512

	// ReasonPermission is the reason recorded for a folder macdoc may not
	// search.
	ReasonPermission = "permission denied (grant Full Disk Access to your terminal)"
)

// Fstatat is the per-entry lstat. Tests replace it to simulate a mount point
// (a different st_dev), which can't be created without root.
var Fstatat = unix.Fstatat

// dir is a directory that is still being listed or still has unfinished
// subdirectories. Finished directories are folded into their parent and
// dropped, so only the active frontier and its ancestors are in memory.
type dir struct {
	parent *dir
	path   string // unmapped absolute path
	ino    uint64
	depth  int
	opaque bool // path is an Opaque root: measured, children never kept
	hidden bool // strict descendant of an Opaque root: never kept
	watch  bool // some Skip or Opaque path is a direct child

	// Guarded by scanner.mu once the dir is published to the stack.
	pending int // 1 for its own listing + unfinished child dirs
	bytes   int64
	files   int64
	mtime   int64
	private privacy // bytes a delete certainly frees, as far as known
}

// privacy is a running sum of private sizes that remembers whether every term
// was known. Once one term is unknown the sum is unknown and stays so as it
// is folded up the tree; the bytes added before and after are never shown as
// the answer, so unknown can't turn into zero.
type privacy struct {
	sum     int64
	unknown bool
}

// note adds e's private size, or marks the sum unknown if e has none.
func (p *privacy) note(e *entry) {
	if e.known {
		p.sum += e.private
	} else {
		p.unknown = true
	}
}

// merge folds in another sum.
func (p *privacy) merge(o privacy) {
	p.sum += o.sum
	p.unknown = p.unknown || o.unknown
}

// value returns the sum for a node, or nil when any term was unknown.
func (p privacy) value() *int64 {
	if p.unknown {
		return nil
	}
	v := p.sum
	return &v
}

// Scanner holds the state of one scan: the directories still to list, the
// kept nodes and the unmeasured paths. Use Scan to run one.
type Scanner struct {
	threshold int64
	keep      func(string) bool
	Prefix    string
	rootDev   int32
	skip      map[string]bool // lowercased unmapped paths
	opaque    map[string]bool // lowercased unmapped paths
	watchDirs map[string]bool // lowercased parents of skip and opaque paths

	stop   atomic.Bool  // set on ctx cancel; checked between batches
	nobulk atomic.Bool  // getattrlistbulk is unsupported on this volume
	files  atomic.Int64 // progress counters
	bytes  atomic.Int64
	cur    atomic.Pointer[dir]

	links LinkSet

	mu         sync.Mutex
	cond       *sync.Cond
	stack      []*dir // LIFO: depth-first keeps the frontier small
	active     int    // workers currently listing a dir
	canceled   bool
	root       *dir // set once the root is finalized
	nodes      []model.Node
	Unmeasured []model.Unmeasured
}

// Scan walks opts.Root. It returns an error only for an unusable root;
// unreadable subdirectories become Unmeasured entries. On ctx cancellation it
// returns the partial Result with Incomplete set and a nil error.
func Scan(ctx context.Context, opts Options) (Result, error) {
	start := time.Now()
	root := opts.Root
	if root == "" {
		root = DataVolume
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Result{}, fmt.Errorf("scanner: root %q: %w", opts.Root, err)
	}
	prefix := opts.MapPrefix
	if prefix == "" && root == DataVolume {
		prefix = DataVolume
	}
	if prefix != "" {
		prefix = filepath.Clean(prefix)
	}

	var st unix.Stat_t
	if err := unix.Lstat(root, &st); err != nil {
		return Result{}, fmt.Errorf("scanner: root %s: %w", root, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return Result{}, fmt.Errorf("scanner: root %s: not a directory", root)
	}

	s := &Scanner{
		threshold: opts.KeepThreshold,
		keep:      opts.Keep,
		Prefix:    prefix,
		rootDev:   st.Dev,
		skip:      folded(opts.Skip),
		opaque:    folded(opts.Opaque),
		links:     LinkSet{Limit: opts.HardlinkLimit},
	}
	if s.threshold <= 0 {
		s.threshold = defaultKeepThreshold
	}
	if s.links.Limit <= 0 {
		s.links.Limit = defaultHardlinkLimit
	}
	s.watchDirs = make(map[string]bool)
	for p := range s.skip {
		s.watchDirs[filepath.Dir(p)] = true
	}
	for p := range s.opaque {
		s.watchDirs[filepath.Dir(p)] = true
	}
	s.cond = sync.NewCond(&s.mu)

	rd := &dir{path: root, ino: st.Ino, pending: 1, bytes: st.Blocks * 512, mtime: st.Mtim.Sec,
		private: privacy{sum: st.Blocks * 512}}
	rd.opaque = s.opaque[pathfold.Fold(root)]
	rd.watch = s.watchDirs[pathfold.Fold(root)]
	s.stack = append(s.stack, rd)

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	workers = min(max(workers, 1), widest)

	if ctx.Err() != nil {
		// Already canceled: don't let a worker start listing first.
		s.stop.Store(true)
		s.canceled = true
	}
	done := make(chan struct{})
	var bg sync.WaitGroup
	bg.Go(func() {
		select {
		case <-ctx.Done():
			s.stop.Store(true)
			s.mu.Lock()
			s.canceled = true
			s.cond.Broadcast()
			s.mu.Unlock()
		case <-done:
		}
	})
	if opts.Progress != nil {
		every := opts.ProgressEvery
		if every <= 0 {
			every = defaultProgressEvery
		}
		bg.Go(func() { s.progress(opts.Progress, every, start, done) })
	}

	var wg sync.WaitGroup
	for range workers {
		wg.Go(s.run)
	}
	wg.Wait()
	close(done)
	bg.Wait()

	// After a cancel, directories still on the stack were never listed.
	// Release them so their own blocks and everything already finished
	// roll up to the root.
	s.mu.Lock()
	for _, d := range s.stack {
		s.release(d)
	}
	s.stack = nil
	s.mu.Unlock()

	sort.Slice(s.nodes, func(i, j int) bool {
		a, b := s.nodes[i], s.nodes[j]
		if a.Bytes != b.Bytes {
			return a.Bytes > b.Bytes
		}
		return a.Path < b.Path
	})
	return Result{
		Root:             s.Map(root),
		TotalBytes:       s.root.bytes,
		TotalFiles:       s.root.files,
		Nodes:            s.nodes,
		Unmeasured:       s.Unmeasured,
		HardlinkLimitHit: s.links.Hit,
		Incomplete:       s.canceled,
		Duration:         time.Since(start),
	}, nil
}

func folded(paths []string) map[string]bool {
	m := make(map[string]bool, len(paths))
	for _, p := range paths {
		if p != "" {
			m[pathfold.Fold(filepath.Clean(p))] = true
		}
	}
	return m
}

// Map applies MapPrefix: prefix itself becomes "/", prefix/x becomes /x.
func (s *Scanner) Map(p string) string {
	if s.Prefix == "" || s.Prefix == "/" {
		return p
	}
	if p == s.Prefix {
		return "/"
	}
	if strings.HasPrefix(p, s.Prefix) && p[len(s.Prefix)] == '/' {
		return p[len(s.Prefix):]
	}
	return p
}

// Join appends name to dirPath without cleaning, treating "/" as the root.
func Join(dirPath, name string) string {
	if dirPath == "/" {
		return "/" + name
	}
	return dirPath + "/" + name
}

func (s *Scanner) progress(fn func(Progress), every time.Duration, start time.Time, done <-chan struct{}) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			p := Progress{Files: s.files.Load(), Bytes: s.bytes.Load(), Elapsed: time.Since(start)}
			if d := s.cur.Load(); d != nil {
				p.CurrentDir = s.Map(d.path)
			}
			fn(p)
		}
	}
}

// worker is one reader goroutine's private state. Kept nodes and child
// directories collect here and reach the shared scanner under one lock per
// batch or directory, not one per file.
type worker struct {
	buf      []byte       // getattrlistbulk reply buffer, allocated on first use
	nodes    []model.Node // kept files of the directory being listed
	children []*dir       // subdirectories found in the current batch
	bytes    int64        // progress counters for the current batch
	files    int64
}

// flush publishes the current batch: progress counters first, then the new
// subdirectories become work for other readers.
func (w *worker) flush(s *Scanner, d *dir) {
	s.bytes.Add(w.bytes)
	s.files.Add(w.files)
	w.bytes, w.files = 0, 0
	if len(w.children) > 0 {
		s.mu.Lock()
		d.pending += len(w.children)
		s.stack = append(s.stack, w.children...)
		s.cond.Broadcast()
		s.mu.Unlock()
		clear(w.children)
		w.children = w.children[:0]
	}
}

func (s *Scanner) run() {
	var w worker
	for {
		d := s.next()
		if d == nil {
			return
		}
		s.list(&w, d)
		s.mu.Lock()
		s.active--
		if s.active == 0 && len(s.stack) == 0 {
			s.cond.Broadcast() // walk finished: wake idle workers to exit
		}
		s.mu.Unlock()
	}
}

// next pops the next directory, waiting while others may still push work.
// It returns nil when the walk is finished or canceled.
func (s *Scanner) next() *dir {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.stack) == 0 && s.active > 0 && !s.canceled {
		s.cond.Wait()
	}
	if s.canceled || len(s.stack) == 0 {
		return nil
	}
	d := s.stack[len(s.stack)-1]
	s.stack[len(s.stack)-1] = nil
	s.stack = s.stack[:len(s.stack)-1]
	s.active++
	return d
}

// totals accumulates one directory's direct entries before they are merged.
type totals struct {
	bytes, files, mtime int64
	private             privacy
}

// merge adds what a worker counted for d to d and the scan, and releases d's
// own pending count.
func (s *Scanner) merge(w *worker, d *dir, acc *totals) {
	s.mu.Lock()
	d.bytes += acc.bytes
	d.files += acc.files
	d.mtime = max(d.mtime, acc.mtime)
	d.private.merge(acc.private)
	s.nodes = append(s.nodes, w.nodes...)
	s.release(d)
	s.mu.Unlock()
	clear(w.nodes)
	w.nodes = w.nodes[:0]
}

// open opens d and checks that it is still the directory its parent saw: the
// same device and inode. Anything else was swapped mid-scan and is ignored.
// ok is false when the directory is not to be read; a failure is recorded.
func (s *Scanner) open(d *dir) (f *os.File, rc syscall.RawConn, ok bool) {
	f, err := os.OpenFile(d.path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		s.Fail(d.path, err)
		return nil, nil, false
	}
	rc, err = f.SyscallConn()
	if err != nil {
		_ = f.Close()
		s.Fail(d.path, err)
		return nil, nil, false
	}
	var st unix.Stat_t
	if cerr := rc.Control(func(fd uintptr) { err = unix.Fstat(int(fd), &st) }); cerr != nil {
		err = cerr
	}
	if err != nil {
		_ = f.Close()
		s.Fail(d.path, err)
		return nil, nil, false
	}
	if st.Dev != s.rootDev || st.Ino != d.ino {
		_ = f.Close()
		return nil, nil, false
	}
	return f, rc, true
}

// list reads d's entries, counts files and pushes subdirectories. It always
// ends by releasing d's own pending count, even on error or cancel.
func (s *Scanner) list(w *worker, d *dir) {
	s.cur.Store(d)
	var acc totals
	defer s.merge(w, d, &acc)
	if s.stop.Load() {
		return
	}
	f, rc, ok := s.open(d)
	if !ok {
		return
	}
	defer f.Close() //nolint:errcheck // read-only directory handle
	var wn *watchNames
	if d.watch {
		wn = s.watch(d)
	}
	if Bulk && !s.nobulk.Load() && s.bulk(w, d, rc, wn, &acc) {
		return
	}
	for !s.stop.Load() {
		names, rerr := f.Readdirnames(batchSize)
		if len(names) > 0 {
			var abort bool
			if cerr := rc.Control(func(fd uintptr) { abort = s.batch(w, d, int(fd), names, wn, &acc) }); cerr != nil {
				s.Fail(d.path, cerr)
				return
			}
			if abort {
				return
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				s.Fail(d.path, rerr)
			}
			return
		}
	}
}

// stat lstats name relative to the open directory fd. ok is false if the
// entry must be skipped; abort is true if the directory can't be read further
// (no search permission, recorded once for the directory).
func (s *Scanner) stat(d *dir, fd int, name string, st *unix.Stat_t) (ok, abort bool) {
	err := Fstatat(fd, name, st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return true, false
	}
	if vanished(err) {
		return false, false
	}
	if errors.Is(err, fs.ErrPermission) {
		s.Fail(d.path, err) // no search permission: one entry for the dir
		return false, true
	}
	s.Fail(Join(d.path, name), err)
	return false, false
}

// batch is the fstatat reader: it stats one batch of names relative to the
// open directory fd. It returns true if the directory should not be read
// further.
func (s *Scanner) batch(w *worker, d *dir, fd int, names []string, wn *watchNames, acc *totals) bool {
	defer w.flush(s, d)
	var st unix.Stat_t // reused: it escapes through the Fstatat hook
	var e entry
	for _, name := range names {
		ok, abort := s.stat(d, fd, name, &st)
		if abort {
			return true
		}
		if !ok {
			continue
		}
		e.fromStat(name, &st)
		s.visit(w, d, &e, wn, acc)
	}
	return false
}

// visit applies one measured entry of d. Both readers feed it, so they can't
// disagree about what an entry means. It touches only w and acc: no lock.
func (s *Scanner) visit(w *worker, d *dir, e *entry, wn *watchNames, acc *totals) {
	var isOpaque bool
	if wn != nil {
		switch wn.match(e.name) {
		case matchSkip:
			return
		case matchOpaque:
			isOpaque = true
		}
	}
	n := e.bytes
	switch e.kind {
	case kindDir:
		if e.dev != s.rootDev {
			return // mount point: another device is never descended or counted
		}
		c := &dir{
			parent:  d,
			path:    Join(d.path, e.name),
			ino:     e.ino,
			depth:   d.depth + 1,
			opaque:  isOpaque,
			hidden:  d.opaque || d.hidden,
			pending: 1,
			bytes:   n,
			mtime:   e.mtime,
			private: privacy{sum: n}, // a folder is not clone-shared: its blocks are its own
		}
		if len(s.watchDirs) > 0 {
			c.watch = s.watchDirs[pathfold.Fold(c.path)]
		}
		w.children = append(w.children, c)
		w.bytes += n
	case kindFile:
		if e.nlink > 1 && !s.links.Count(e.dev, e.ino, e.nlink) {
			return // another link to this inode was already counted
		}
		acc.bytes += n
		acc.files++
		acc.mtime = max(acc.mtime, e.mtime)
		acc.private.note(e) // counted exactly when its bytes are, hard links included
		w.bytes += n
		w.files++
		if n >= s.threshold && !d.opaque && !d.hidden {
			w.nodes = append(w.nodes, model.Node{
				Path:     s.Map(Join(d.path, e.name)),
				Kind:     model.NodeFile,
				Bytes:    n,
				Files:    1,
				MtimeMax: e.mtime,
				Depth:    d.depth + 1,
				Private:  e.value(),
			})
		}
	default: // symlinks (never followed), sockets, fifos: own blocks only
		acc.bytes += n
		acc.mtime = max(acc.mtime, e.mtime)
		acc.private.note(e)
		w.bytes += n
	}
}

// Results of watchNames.match.
const (
	matchNone = iota
	matchSkip
	matchOpaque
)

// watchNames holds, for one directory, the lowercased names of its direct
// children that are Skip or Opaque paths. Matching an entry needs no
// allocation for ASCII names, which is nearly all of them.
type watchNames struct {
	skip, opaque []string
}

// watch builds the watch list of d, a directory with a Skip or Opaque
// child. A child matches when lower(d.path)/lower(name) is a configured path,
// the same rule as the lowercased full-path lookup it replaces.
func (s *Scanner) watch(d *dir) *watchNames {
	dl := pathfold.Fold(d.path)
	wn := &watchNames{}
	collect := func(set map[string]bool) []string {
		var out []string
		for p := range set {
			if p == dl || filepath.Dir(p) != dl {
				continue
			}
			if base := strings.TrimPrefix(p[len(dl):], "/"); base != "" {
				out = append(out, base)
			}
		}
		return out
	}
	wn.skip = collect(s.skip)
	wn.opaque = collect(s.opaque)
	return wn
}

func (wn *watchNames) match(name string) int {
	ascii := true
	for i := 0; i < len(name); i++ {
		if name[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		if among(wn.skip, name) {
			return matchSkip
		}
		if among(wn.opaque, name) {
			return matchOpaque
		}
		return matchNone
	}
	l := pathfold.Fold(name)
	for _, c := range wn.skip {
		if c == l {
			return matchSkip
		}
	}
	for _, c := range wn.opaque {
		if c == l {
			return matchOpaque
		}
	}
	return matchNone
}

// among reports whether the ASCII name, lowercased, equals one of cands
// (already lowercase). It allocates nothing.
func among(cands []string, name string) bool {
	for _, c := range cands {
		if len(c) != len(name) {
			continue
		}
		i := 0
		for ; i < len(name); i++ {
			b := name[i]
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if b != c[i] {
				break
			}
		}
		if i == len(name) {
			return true
		}
	}
	return false
}

// release drops one pending count from d. When it reaches zero, d is
// finished: it is kept if the rules say so and folded into its parent,
// which may finish in turn. Caller holds s.mu.
func (s *Scanner) release(d *dir) {
	for d != nil {
		d.pending--
		if d.pending > 0 {
			return
		}
		if d.parent == nil || (!d.hidden && (d.bytes >= s.threshold || (s.keep != nil && s.keep(s.Map(d.path))))) {
			s.nodes = append(s.nodes, model.Node{
				Path:     s.Map(d.path),
				Kind:     model.NodeDir,
				Bytes:    d.bytes,
				Files:    d.files,
				MtimeMax: d.mtime,
				Depth:    d.depth,
				Private:  d.private.value(),
			})
		}
		p := d.parent
		if p == nil {
			s.root = d
			return
		}
		p.bytes += d.bytes
		p.files += d.files
		p.mtime = max(p.mtime, d.mtime)
		p.private.merge(d.private)
		d = p
	}
}

// vanished reports errors meaning the entry disappeared or was replaced
// mid-scan. These are ignored silently.
func vanished(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP)
}

// Fail records p as unmeasured unless it simply vanished.
func (s *Scanner) Fail(p string, err error) {
	if vanished(err) {
		return
	}
	reason := err.Error()
	var pe *fs.PathError
	if errors.Is(err, fs.ErrPermission) {
		reason = ReasonPermission
	} else if errors.As(err, &pe) {
		reason = pe.Err.Error() // the path is already in Unmeasured.Path
	}
	s.mu.Lock()
	s.Unmeasured = append(s.Unmeasured, model.Unmeasured{Path: s.Map(p), Reason: reason})
	s.mu.Unlock()
}

// LinkSet de-duplicates hard links by (dev, ino). An entry is dropped once
// all of its links have been seen, so only partially seen inodes use memory.
type LinkSet struct {
	mu    sync.Mutex
	Limit int
	M     map[linkKey]uint16 // remaining links not yet seen
	Hit   bool
}

type linkKey struct {
	dev int32
	ino uint64
}

// Count reports whether this link's blocks should be counted.
func (l *LinkSet) Count(dev int32, ino uint64, nlink uint16) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := linkKey{dev, ino}
	if rem, ok := l.M[k]; ok {
		if rem <= 1 {
			delete(l.M, k)
		} else {
			l.M[k] = rem - 1
		}
		return false
	}
	if len(l.M) >= l.Limit {
		l.Hit = true
		return true
	}
	if l.M == nil {
		l.M = make(map[linkKey]uint16)
	}
	l.M[k] = nlink - 1
	return true
}

// SetBackgroundPriority lowers the current process to the macOS background
// band (CPU and I/O throttled) via setpriority(PRIO_DARWIN_PROCESS, 0,
// PRIO_DARWIN_BG). No exec, no cgo. Returns an error if the call fails.
func SetBackgroundPriority() error {
	if err := unix.Setpriority(unix.PRIO_DARWIN_PROCESS, 0, unix.PRIO_DARWIN_BG); err != nil {
		return fmt.Errorf("scanner: setpriority: %w", err)
	}
	return nil
}
