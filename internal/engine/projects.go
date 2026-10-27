package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/projects"
	"macdoc/internal/store"
)

// ProjectsResult is the data behind `macdoc doctor projects`.
type ProjectsResult struct {
	Scan        model.ScanInfo         `json:"scan"`
	ScanAge     string                 `json:"scan_age"`
	Projects    []projects.Project     `json:"projects"`
	DerivedData []projects.DerivedData `json:"derived_data"`
	Warnings    []string               `json:"warnings,omitempty"`
}

// Projects finds the projects in the latest complete scan: their artifact
// folders, how long each has been idle and their git state. It is read-only
// and never starts a scan.
func Projects(ctx context.Context, o OpportunityOptions) (ProjectsResult, error) {
	if o.Home == "" {
		return ProjectsResult{}, errors.New("projects: home directory is required")
	}
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	st, err := store.Open(o.StorePath)
	if err != nil {
		return ProjectsResult{}, err
	}
	defer func() { _ = st.Close() }()
	info, err := st.LatestScan(model.ScanComplete)
	if errors.Is(err, store.ErrNoScan) {
		return ProjectsResult{}, ErrNoScan
	}
	if err != nil {
		return ProjectsResult{}, err
	}
	r, err := discoverProjects(ctx, o, scanNodes{st: st, scanID: info.ID}, info.ID)
	if err != nil {
		return ProjectsResult{}, err
	}
	return ProjectsResult{
		Scan: info, ScanAge: FormatAge(o.Now.Sub(scanTime(info))),
		Projects: r.Projects, DerivedData: r.DerivedData, Warnings: r.Warnings,
	}, nil
}

// projectCacheTTL bounds how long a discovery result is reused within one
// process (the dashboard asks for projects and opportunities together; git
// state can change, so it isn't kept long).
const projectCacheTTL = 2 * time.Minute

var projectCache struct {
	sync.Mutex
	key string
	at  time.Time
	res projects.Result
}

// discoverProjects runs project discovery for a scan, reusing a recent
// result for the same home and scan.
func discoverProjects(ctx context.Context, o OpportunityOptions, nodes projects.NodeSource, scanID string) (projects.Result, error) {
	key := o.Home + "\x00" + o.StorePath + "\x00" + scanID
	projectCache.Lock()
	if projectCache.key == key && time.Since(projectCache.at) < projectCacheTTL {
		r := projectCache.res
		projectCache.Unlock()
		return r, nil
	}
	projectCache.Unlock()

	r, err := projects.Discover(ctx, projects.Env{Home: o.Home, Nodes: nodes, Runner: o.Runner, Now: o.Now}, projects.Options{})
	if err != nil {
		return r, err
	}
	projectCache.Lock()
	projectCache.key, projectCache.at, projectCache.res = key, time.Now(), r
	projectCache.Unlock()
	return r, nil
}
