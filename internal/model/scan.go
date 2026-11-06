package model

import "time"

// NodeKind distinguishes kept directories from kept individual files.
type NodeKind string

// The kinds of node a scan keeps.
const (
	NodeDir  NodeKind = "dir"
	NodeFile NodeKind = "file"
)

// Node is one kept entry from a scan: a directory (with everything below it
// aggregated) or a single large file. Paths are user-facing (firmlinks mapped
// back, e.g. /Users/x rather than /System/Volumes/Data/Users/x).
type Node struct {
	Path     string   `json:"path"`
	Kind     NodeKind `json:"kind"`
	Bytes    int64    `json:"bytes"`     // allocated bytes (st_blocks*512), hard links counted once
	Files    int64    `json:"files"`     // regular files below (1 for a file node)
	MtimeMax int64    `json:"mtime_max"` // newest mtime below, unix seconds
	Depth    int      `json:"depth"`     // components below the scan root

	// Private is the bytes that deleting everything below this node is sure
	// to free on APFS: the sum of its files' private sizes
	// (ATTR_CMNEXT_PRIVATESIZE) plus the allocation of its folders. It is a
	// lower bound, never above Bytes: files that share blocks only with each
	// other inside the node count 0. Nil means unknown, because some file
	// below was measured without a private size, and unknown is never zero.
	// Stage 1 of docs/adr/0005-size-by-allocated-blocks.md stores it and
	// shows it nowhere.
	Private *int64 `json:"-"`
}

// Unmeasured is a path the scanner could not read.
type Unmeasured struct {
	Path   string `json:"path"`
	Reason string `json:"reason"` // e.g. "permission denied (grant Full Disk Access)"
}

// ScanStatus is the lifecycle state of a stored scan.
type ScanStatus string

// The states of a scan.
const (
	ScanRunning    ScanStatus = "running"
	ScanComplete   ScanStatus = "complete"
	ScanIncomplete ScanStatus = "incomplete" // canceled or failed part-way
)

// ScanInfo summarizes one stored scan.
type ScanInfo struct {
	ID              string     `json:"id"`
	Root            string     `json:"root"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      time.Time  `json:"finished_at"`
	Status          ScanStatus `json:"status"`
	TotalBytes      int64      `json:"total_bytes"`
	TotalFiles      int64      `json:"total_files"`
	UnmeasuredCount int        `json:"unmeasured_count"`
	MacdocVersion   string     `json:"macdoc_version"`
}
