package engine

import (
	"context"
	"time"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/probe"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/store"
)

// FreeSpace is the boot container's free space (not counting purgeable),
// for apply's before/after report.
func FreeSpace(ctx context.Context, r platform.Runner) (int64, error) {
	d, err := probe.CollectDisk(ctx, r)
	if err != nil {
		return 0, err
	}
	return d.ContainerFreeBytes, nil
}

// QuarantineResult is the data behind `macdoc quarantine list`.
type QuarantineResult struct {
	quarantine.Listing
	Root         string `json:"root"`
	HeldBytes    int64  `json:"held_bytes"`
	ExpiredBytes int64  `json:"expired_bytes"`
	ExpiredCount int    `json:"expired_count"`
}

// Quarantine lists macdoc's quarantine without changing it. Expired entries
// are reported, never purged.
func Quarantine(o OpportunityOptions) (QuarantineResult, error) {
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	st, err := store.Open(o.StorePath)
	if err != nil {
		return QuarantineResult{}, err
	}
	defer func() { _ = st.Close() }()
	root := quarantine.Root(o.StorePath)
	l, err := quarantine.List(st, root, o.Now)
	if err != nil {
		return QuarantineResult{}, err
	}
	out := QuarantineResult{Listing: l, Root: root, HeldBytes: l.Held()}
	for _, e := range l.Expired() {
		out.ExpiredBytes += e.Record.Bytes
		out.ExpiredCount++
	}
	return out, nil
}

// AuditResult is the data behind `macdoc log`.
type AuditResult struct {
	Path     string         `json:"path"`
	Records  []audit.Record `json:"records"`
	BadLines int            `json:"bad_lines"`
}

// AuditLog returns the last limit audit records (all if limit <= 0).
func AuditLog(o OpportunityOptions, limit int) (AuditResult, error) {
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	p := audit.Path(o.StorePath)
	recs, bad, err := audit.Read(p, limit)
	return AuditResult{Path: p, Records: recs, BadLines: bad}, err
}
