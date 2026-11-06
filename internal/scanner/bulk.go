package scanner

import (
	"encoding/binary"
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Bulk selects the getattrlistbulk(2) directory reader. When false (or when
// the volume does not support the call) every entry is measured with one
// fstatat call instead. Both readers produce identical results; tests switch
// it off to use the Fstatat hook.
var Bulk = true

// Attribute bits that golang.org/x/sys/unix does not export.
const (
	attrDirMountStatus = 0x00000004 // ATTR_DIR_MOUNTSTATUS
	attrDirAllocSize   = 0x00000008 // ATTR_DIR_ALLOCSIZE

	// attrPrivateSize is ATTR_CMNEXT_PRIVATESIZE: the bytes freed if this
	// file were deleted. It travels in the forkattr field and is honored only
	// with FSOPT_ATTR_CMN_EXTENDED.
	attrPrivateSize = 0x00000008

	// attrCloneID is ATTR_CMNEXT_CLONEID: files that share it are clones of
	// each other and hold the same data. attrCloneRefs is
	// ATTR_CMNEXT_CLONE_REFCNT: how many files share it. Both travel in
	// forkattr after the private size, in bit order. A full scan does not ask
	// for them (they cost 5 to 8%); only Measure does.
	attrCloneID   = 0x00000100
	attrCloneRefs = 0x00001000

	// fsobj_type_t values from <sys/vnode.h>, which is not a public header.
	vreg = 1
	vdir = 2

	// buffer is each worker's reply buffer. One call returns as many
	// entries as fit, so a typical directory takes one or two calls.
	buffer = 128 << 10
)

// bulkAttrs is the fixed request. Besides what the walk needs (name, device,
// type, inode, mtime, link count, allocated and private size) it asks for the per-entry
// error, file flags (SF_FIRMLINK) and the directory mount status, so that
// every case where the kernel's answer could differ from lstat's is sent to
// the fstatat path for that one entry instead of being guessed.
var bulkAttrs = unix.Attrlist{
	Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
	Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_ERROR | unix.ATTR_CMN_NAME |
		unix.ATTR_CMN_DEVID | unix.ATTR_CMN_OBJTYPE | unix.ATTR_CMN_MODTIME |
		unix.ATTR_CMN_FLAGS | unix.ATTR_CMN_FILEID,
	Dirattr:  attrDirMountStatus | attrDirAllocSize,
	Fileattr: unix.ATTR_FILE_LINKCOUNT | unix.ATTR_FILE_ALLOCSIZE,
	Forkattr: attrPrivateSize,
}

// cloneAttrs is bulkAttrs plus the clone ID and its reference count, for the
// on-demand walk of one folder (see Measure).
var cloneAttrs = func() unix.Attrlist {
	a := bulkAttrs
	a.Forkattr |= attrCloneID | attrCloneRefs
	return a
}()

// Entry kinds. Anything that is neither a directory nor a regular file
// (symlink, socket, fifo, device) counts its own blocks only.
const (
	kindOther uint8 = iota
	kindDir
	kindFile
)

// entry is one measured directory entry, whichever reader produced it. Only
// the bulk reader learns a private size (S1); the fstatat one leaves known
// false, and an entry that is not known is never read as zero.
type entry struct {
	name  string // valid only until the reader's next call; copy to keep
	kind  uint8
	dev   int32
	ino   uint64
	nlink uint16
	bytes int64 // allocated bytes (st_blocks * 512)
	mtime int64 // seconds

	private int64  // bytes freed by deleting this file; meaningful only if known
	known   bool   // the kernel reported private
	clone   uint64 // clone ID; meaningful only if refs > 0
	refs    uint32 // files that share the clone ID; 0 when the kernel did not say
}

// freed returns what deleting this entry is certain to free, and whether
// that is known. A file with several links is certain to free nothing: the
// kernel reports its full size for every link, and a tree that holds only
// some of them would overstate (S1 scenario 5). That is a known zero, not an
// unknown, so long as the kernel reported a private size at all.
func (e *entry) freed() (int64, bool) {
	if !e.known {
		return 0, false
	}
	if e.nlink > 1 {
		return 0, true
	}
	return e.private, true
}

// value returns freed as a pointer, nil when unknown.
func (e *entry) value() *int64 {
	v, ok := e.freed()
	if !ok {
		return nil
	}
	return &v
}

// fromStat fills e from an lstat result.
func (e *entry) fromStat(name string, st *unix.Stat_t) {
	*e = entry{
		name:  name,
		dev:   st.Dev,
		ino:   st.Ino,
		nlink: st.Nlink,
		bytes: st.Blocks * 512,
		mtime: st.Mtim.Sec,
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		e.kind = kindDir
	case unix.S_IFREG:
		e.kind = kindFile
	}
}

// getattrlistbulk calls getattrlistbulk(2) on a directory descriptor with the
// requested attributes and returns the number of entries written to buf (0 at the end).
func getattrlistbulk(fd int, buf []byte, attrs *unix.Attrlist) (int, error) {
	for {
		// attrs is never freed and buf is a live heap slice for the whole call.
		r, _, errno := unix.Syscall6(unix.SYS_GETATTRLISTBULK, uintptr(fd), uintptr(unsafe.Pointer(attrs)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), unix.FSOPT_NOFOLLOW|unix.FSOPT_ATTR_CMN_EXTENDED, 0) //nolint:staticcheck,gosec // SA1019, G103: raw syscall is the only cgo-free route
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return 0, errno
		}
		return int(r), nil
	}
}

// cursor reads little-endian values from one reply record without panicking
// on a truncated one: after any short read bad is set and values are zero.
type cursor struct {
	b   []byte
	p   int
	bad bool
}

func (c *cursor) u32() uint32 {
	if c.p+4 > len(c.b) {
		c.bad = true
		return 0
	}
	v := binary.NativeEndian.Uint32(c.b[c.p:])
	c.p += 4
	return v
}

func (c *cursor) u64() uint64 {
	if c.p+8 > len(c.b) {
		c.bad = true
		return 0
	}
	v := binary.NativeEndian.Uint64(c.b[c.p:])
	c.p += 8
	return v
}

// decode parses one reply record into e. full is false when the record is
// valid but lacks something the walk needs, or describes a case that lstat
// might answer differently (a per-entry error, a mount point, a firmlink); the
// caller then measures that entry with fstatat. ok is false if the record is
// malformed or has no name.
func decode(rec []byte, e *entry) (full, ok bool) {
	c := cursor{b: rec, p: 4}
	common := c.u32()
	_ = c.u32() // volume attributes: never requested
	dirattr := c.u32()
	fileattr := c.u32()
	forkattr := c.u32() // with FSOPT_ATTR_CMN_EXTENDED: the common-extension bits
	var errno uint32
	if common&unix.ATTR_CMN_ERROR != 0 {
		errno = c.u32()
	}
	name, ok := entryName(&c, rec, common)
	if !ok {
		return false, false
	}
	*e = entry{name: name}
	if errno != 0 {
		return false, true
	}
	m, have := commonAttrs(&c, common)
	if !have || c.bad || m.flags&unix.SF_FIRMLINK != 0 {
		return false, true
	}
	e.dev = int32(m.dev) //nolint:gosec // G115: a device number from the kernel fits in int32
	e.ino = m.ino
	e.mtime = int64(m.mtime) //nolint:gosec // G115: seconds since the epoch fit in int64
	if m.objtype == vdir {
		return dirSize(&c, e, dirattr, forkattr), true
	}
	return fileSize(&c, e, m.objtype, fileattr, forkattr), true
}

// entryName reads the name of a record. ok is false if it is missing,
// empty or points outside the record.
func entryName(c *cursor, rec []byte, common uint32) (name string, ok bool) {
	if common&unix.ATTR_CMN_NAME == 0 {
		return "", false
	}
	// An attrreference's offset is relative to the reference itself.
	at := c.p
	off := int32(c.u32()) //nolint:gosec // G115: the kernel's signed offset; bounds-checked below
	n := int(c.u32())
	start := at + int(off)
	if c.bad || off < 0 || n < 1 || start+n > len(rec) {
		return "", false
	}
	nameBytes := rec[start : start+n]
	if i := nul(nameBytes); i >= 0 {
		nameBytes = nameBytes[:i]
	}
	if len(nameBytes) == 0 {
		return "", false
	}
	// The record stays alive and unchanged until the next getattrlistbulk
	// call; every use of the name copies it (path joins) before then.
	return unsafe.String(&nameBytes[0], len(nameBytes)), true //nolint:gosec // G103: see comment above
}

// meta is the common attributes of a record.
type meta struct {
	dev, objtype, flags uint32
	mtime, ino          uint64
}

// commonAttrs reads the common attributes that follow the name. have is false
// when one the walk needs was not returned.
func commonAttrs(c *cursor, common uint32) (m meta, have bool) {
	have = common&unix.ATTR_CMN_DEVID != 0
	if have {
		m.dev = c.u32()
	}
	if common&unix.ATTR_CMN_OBJTYPE == 0 {
		have = false
	} else {
		m.objtype = c.u32()
	}
	if common&unix.ATTR_CMN_MODTIME == 0 {
		have = false
	} else {
		m.mtime = c.u64() // timespec: seconds, then nanoseconds
		_ = c.u64()
	}
	if common&unix.ATTR_CMN_FLAGS == 0 {
		have = false
	} else {
		m.flags = c.u32()
	}
	if common&unix.ATTR_CMN_FILEID == 0 {
		have = false
	} else {
		m.ino = c.u64()
	}
	return m, have
}

// dirSize reads a directory's own allocation. It returns false when lstat
// should answer instead: a mount point or automount trigger, or an attribute
// that was not returned.
func dirSize(c *cursor, e *entry, dirattr, forkattr uint32) bool {
	if dirattr&attrDirMountStatus == 0 || dirattr&attrDirAllocSize == 0 {
		return false
	}
	if c.u32() != 0 { // a mount point or automount trigger
		return false
	}
	e.bytes = int64(c.u64()) //nolint:gosec // G115: allocated bytes fit in int64
	e.kind = kindDir
	_ = forkValues(c, forkattr) // a folder's private size is its own blocks, already known
	return !c.bad
}

// fileSize reads a non-directory's allocation, link count and private size.
// A private size the kernel did not return leaves e.known false.
func fileSize(c *cursor, e *entry, objtype, fileattr, forkattr uint32) bool {
	if fileattr&unix.ATTR_FILE_ALLOCSIZE == 0 {
		return false
	}
	var nlink uint32
	if fileattr&unix.ATTR_FILE_LINKCOUNT != 0 {
		nlink = c.u32()
	} else if objtype == vreg {
		return false
	}
	e.bytes = int64(c.u64()) //nolint:gosec // G115: allocated bytes fit in int64
	e.nlink = uint16(nlink & 0xffff)
	fv := forkValues(c, forkattr)
	e.private, e.known = fv.private, fv.have && !c.bad
	e.clone, e.refs = fv.clone, fv.refs
	if objtype == vreg {
		e.kind = kindFile
	}
	return !c.bad
}

// fork is the common-extension attributes of one record.
type fork struct {
	private int64
	have    bool // the private size was returned
	clone   uint64
	refs    uint32
}

// forkValues reads the extension attributes the record says it carries, in
// the order the kernel writes them (ascending bit).
func forkValues(c *cursor, forkattr uint32) (f fork) {
	if forkattr&attrPrivateSize != 0 {
		f.private = int64(c.u64()) //nolint:gosec // G115: allocated bytes fit in int64
		f.have = true
	}
	if forkattr&attrCloneID != 0 {
		f.clone = c.u64()
	}
	if forkattr&attrCloneRefs != 0 {
		f.refs = c.u32()
	}
	return f
}

func nul(b []byte) int {
	for i, x := range b {
		if x == 0 {
			return i
		}
	}
	return -1
}

// unsupported reports errors meaning this volume cannot do getattrlistbulk.
func unsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSYS)
}

// bulk reads d with getattrlistbulk. It returns false only when the call
// is unsupported on this volume and nothing was read, so the caller can fall
// back to the fstatat path for the same open directory.
func (s *Scanner) bulk(w *worker, d *dir, rc syscall.RawConn, wn *watchNames, acc *totals) bool {
	if w.buf == nil {
		w.buf = make([]byte, buffer)
	}
	done := true
	cerr := rc.Control(func(fdu uintptr) {
		fd := int(fdu)
		first := true
		for !s.stop.Load() {
			n, err := getattrlistbulk(fd, w.buf, s.attrs)
			if err != nil {
				if first && unsupported(err) {
					s.nobulk.Store(true)
					done = false
					return
				}
				s.Fail(d.path, err)
				return
			}
			if n == 0 {
				return
			}
			first = false
			if s.decodeBatch(w, d, fd, n, wn, acc) {
				return
			}
		}
	})
	if cerr != nil {
		s.Fail(d.path, cerr)
	}
	return done
}

// decodeBatch handles the n records in w.buf. It returns true if the
// directory should not be read further.
func (s *Scanner) decodeBatch(w *worker, d *dir, fd, n int, wn *watchNames, acc *totals) bool {
	defer w.flush(s, d)
	var e entry
	var st unix.Stat_t
	buf := w.buf
	for range n {
		if len(buf) < 4 {
			s.Fail(d.path, syscall.EIO)
			return true
		}
		length := int(binary.NativeEndian.Uint32(buf))
		if length < 28 || length > len(buf) {
			s.Fail(d.path, syscall.EIO) // a record the kernel never writes
			return true
		}
		rec := buf[:length]
		buf = buf[length:]
		full, ok := decode(rec, &e)
		if !ok {
			continue
		}
		if e.name == "." || e.name == ".." {
			continue
		}
		if !full {
			// Measure just this entry the old way, with the same handling
			// of vanished, unreadable and mount-point entries.
			name := e.name
			ok, abort := s.stat(d, fd, name, &st)
			if abort {
				return true
			}
			if !ok {
				continue
			}
			e.fromStat(name, &st)
		}
		s.visit(w, d, &e, wn, acc)
	}
	return false
}
