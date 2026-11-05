// Package scanner walks the Data volume and measures allocated space with
// bounded memory, at background priority. It is strictly read-only.
//
// CONTRACT (tech lead): exported names and documented behavior are fixed.
// Implementation: task P2-W1 (docs/tasks/P2-W1.md).
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

	"macdoc/internal/model"
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
	maxWorkers           = 16
	defaultHardlinkLimit = 1_000_000
	defaultProgressEvery = 250 * time.Millisecond

	// batchSize is how many names are read from a directory at a time, so a
	// directory with millions of entries never sits in memory at once and
	// cancellation is noticed between batches.
	batchSize = 512

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
}

type Scanner struct {
	threshold int64
	keep      func(string) bool
	Prefix    string
	rootDev   int32
	skip      map[string]bool // lowercased unmapped paths
	opaque    map[string]bool // lowercased unmapped paths
	watchDirs map[string]bool // lowercased parents of skip and opaque paths

	stop  atomic.Bool  // set on ctx cancel; checked between batches
	files atomic.Int64 // progress counters
	bytes atomic.Int64
	cur   atomic.Pointer[dir]

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
		skip:      lowerSet(opts.Skip),
		opaque:    lowerSet(opts.Opaque),
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

	rd := &dir{path: root, ino: st.Ino, pending: 1, bytes: st.Blocks * 512, mtime: st.Mtim.Sec}
	rd.opaque = s.opaque[strings.ToLower(root)]
	rd.watch = s.watchDirs[strings.ToLower(root)]
	s.stack = append(s.stack, rd)

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}
	workers = min(max(workers, 1), maxWorkers)

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
		wg.Go(s.worker)
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
		Root:             s.MapPath(root),
		TotalBytes:       s.root.bytes,
		TotalFiles:       s.root.files,
		Nodes:            s.nodes,
		Unmeasured:       s.Unmeasured,
		HardlinkLimitHit: s.links.Hit,
		Incomplete:       s.canceled,
		Duration:         time.Since(start),
	}, nil
}

func lowerSet(paths []string) map[string]bool {
	m := make(map[string]bool, len(paths))
	for _, p := range paths {
		if p != "" {
			m[strings.ToLower(filepath.Clean(p))] = true
		}
	}
	return m
}

// MapPath applies MapPrefix: prefix itself becomes "/", prefix/x becomes /x.
func (s *Scanner) MapPath(p string) string {
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
				p.CurrentDir = s.MapPath(d.path)
			}
			fn(p)
		}
	}
}

func (s *Scanner) worker() {
	for {
		d := s.next()
		if d == nil {
			return
		}
		s.list(d)
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
}

// list reads d's entries, counts files and pushes subdirectories. It always
// ends by releasing d's own pending count, even on error or cancel.
func (s *Scanner) list(d *dir) {
	s.cur.Store(d)
	var acc totals
	defer func() {
		s.mu.Lock()
		d.bytes += acc.bytes
		d.files += acc.files
		d.mtime = max(d.mtime, acc.mtime)
		s.release(d)
		s.mu.Unlock()
	}()
	if s.stop.Load() {
		return
	}
	f, err := os.OpenFile(d.path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		s.Fail(d.path, err)
		return
	}
	defer f.Close() //nolint:errcheck // read-only directory handle
	rc, err := f.SyscallConn()
	if err != nil {
		s.Fail(d.path, err)
		return
	}
	// The directory must still be the one its parent saw: same device and
	// inode. Anything else was swapped mid-scan and is ignored.
	var st unix.Stat_t
	if cerr := rc.Control(func(fd uintptr) { err = unix.Fstat(int(fd), &st) }); cerr != nil {
		err = cerr
	}
	if err != nil {
		s.Fail(d.path, err)
		return
	}
	if st.Dev != s.rootDev || st.Ino != d.ino {
		return
	}
	for !s.stop.Load() {
		names, rerr := f.Readdirnames(batchSize)
		if len(names) > 0 {
			var abort bool
			if cerr := rc.Control(func(fd uintptr) { abort = s.batch(d, int(fd), names, &acc) }); cerr != nil {
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

// batch stats one batch of names relative to the open directory fd. It
// returns true if the directory should not be read further.
func (s *Scanner) batch(d *dir, fd int, names []string, acc *totals) bool {
	var children []*dir
	var nbytes, nfiles int64
	defer func() {
		s.bytes.Add(nbytes)
		s.files.Add(nfiles)
		if len(children) > 0 {
			s.mu.Lock()
			d.pending += len(children)
			s.stack = append(s.stack, children...)
			s.cond.Broadcast()
			s.mu.Unlock()
		}
	}()
	var st unix.Stat_t // reused: it escapes through the Fstatat hook
	for _, name := range names {
		if err := Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if vanished(err) {
				continue
			}
			if errors.Is(err, fs.ErrPermission) {
				s.Fail(d.path, err) // no search permission: one entry for the dir
				return true
			}
			s.Fail(Join(d.path, name), err)
			continue
		}
		var isOpaque bool
		if d.watch {
			lp := strings.ToLower(Join(d.path, name))
			if s.skip[lp] {
				continue
			}
			isOpaque = s.opaque[lp]
		}
		n := st.Blocks * 512
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			if st.Dev != s.rootDev {
				continue // mount point: another device is never descended or counted
			}
			c := &dir{
				parent:  d,
				path:    Join(d.path, name),
				ino:     st.Ino,
				depth:   d.depth + 1,
				opaque:  isOpaque,
				hidden:  d.opaque || d.hidden,
				pending: 1,
				bytes:   n,
				mtime:   st.Mtim.Sec,
			}
			if len(s.watchDirs) > 0 {
				c.watch = s.watchDirs[strings.ToLower(c.path)]
			}
			children = append(children, c)
			nbytes += n
		case unix.S_IFREG:
			if st.Nlink > 1 && !s.links.Count(st.Dev, st.Ino, st.Nlink) {
				continue // another link to this inode was already counted
			}
			acc.bytes += n
			acc.files++
			acc.mtime = max(acc.mtime, st.Mtim.Sec)
			nbytes += n
			nfiles++
			if n >= s.threshold && !d.opaque && !d.hidden {
				s.mu.Lock()
				s.nodes = append(s.nodes, model.Node{
					Path:     s.MapPath(Join(d.path, name)),
					Kind:     model.NodeFile,
					Bytes:    n,
					Files:    1,
					MtimeMax: st.Mtim.Sec,
					Depth:    d.depth + 1,
				})
				s.mu.Unlock()
			}
		default: // symlinks (never followed), sockets, fifos: own blocks only
			acc.bytes += n
			acc.mtime = max(acc.mtime, st.Mtim.Sec)
			nbytes += n
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
		if d.parent == nil || (!d.hidden && (d.bytes >= s.threshold || (s.keep != nil && s.keep(s.MapPath(d.path))))) {
			s.nodes = append(s.nodes, model.Node{
				Path:     s.MapPath(d.path),
				Kind:     model.NodeDir,
				Bytes:    d.bytes,
				Files:    d.files,
				MtimeMax: d.mtime,
				Depth:    d.depth,
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
		d = p
	}
}

// vanished reports errors meaning the entry disappeared or was replaced
// mid-scan. These are ignored silently.
func vanished(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP)
}

// fail records p as unmeasured unless it simply vanished.
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
	s.Unmeasured = append(s.Unmeasured, model.Unmeasured{Path: s.MapPath(p), Reason: reason})
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

// count reports whether this link's blocks should be counted.
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
