package recipes

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"macdoc/internal/model"
)

var (
	regMu     sync.RWMutex
	detectors = map[string]Detector{}
)

// Register adds a detector under name. It panics on a duplicate name; call
// it from init().
func Register(name string, d Detector) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := detectors[name]; dup {
		panic("recipes: duplicate detector " + name)
	}
	detectors[name] = d
}

// Lookup returns the detector registered under name.
func Lookup(name string) (Detector, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	d, ok := detectors[name]
	return d, ok
}

// DetectorNames returns all registered detector names, sorted.
func DetectorNames() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	names := make([]string, 0, len(detectors))
	for n := range detectors {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ExpandHome replaces a leading "~" or "~/" with home.
func ExpandHome(home, p string) string {
	switch {
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	}
	return p
}

// ToolInstalled reports whether a native tool is on PATH (read-only lookup).
// Detectors for native recipes use it to return nil findings when the tool
// isn't installed.
var ToolInstalled = func(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func init() {
	Register("children", detectChildren)
	Register("path", detectPath)
}

// detectChildren emits one finding per kept direct child of params["dir"]
// (from the scan), sized from the scan, LastUsed = newest mtime below.
// Optional params["min_bytes"] (default 1) skips tiny children.
func detectChildren(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dir := r.Params["dir"]
	if dir == "" {
		return nil, fmt.Errorf("recipe %s: detector children needs params.dir", r.ID)
	}
	if env.Nodes == nil {
		return nil, nil
	}
	dir = ExpandHome(env.Home, dir)
	nodes, err := env.Nodes.Nodes(dir, 1)
	if err != nil {
		return nil, err
	}
	minBytes, err := paramInt(r, "min_bytes", 1)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range nodes {
		if n.Path == dir || n.Bytes < minBytes {
			continue
		}
		out = append(out, nodeFinding(n))
	}
	return out, nil
}

// detectPath emits one finding for params["path"] if the scan kept it.
func detectPath(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	p := r.Params["path"]
	if p == "" {
		return nil, fmt.Errorf("recipe %s: detector path needs params.path", r.ID)
	}
	if env.Nodes == nil {
		return nil, nil
	}
	p = ExpandHome(env.Home, p)
	nodes, err := env.Nodes.Nodes(p, 0)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if strings.EqualFold(n.Path, p) && n.Bytes > 0 {
			return []Finding{nodeFinding(n)}, nil
		}
	}
	return nil, nil
}

// nodeFinding converts a scan node into a finding sized by the scan.
func nodeFinding(n model.Node) Finding {
	f := Finding{Target: n.Path, Bytes: model.Bytes{Value: n.Bytes, Source: model.SourceMeasured}}
	if n.MtimeMax > 0 {
		f.LastUsed = unixTime(n.MtimeMax)
	}
	return f
}
