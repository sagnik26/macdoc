// Package inventory finds the software installed on this Mac and the folders
// in a scan that belong to it. It only reads: every query is a fixed,
// read-only command through platform.Runner, and nothing it finds is removed
// here (ADR 0007).
package inventory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sagnik26/macdoc/internal/platform"
)

// Budget bounds one source. A source that doesn't finish in time is reported
// as unknown; its software is missing, never counted as zero.
const Budget = 10 * time.Second

// Software is one installed program, app or package.
type Software struct {
	ID        string    // stable within one run: "app:<bundle id>", "brew:<formula>", "npm:<package>", "app-name:<name>"
	Name      string    // the name a person knows it by
	Kind      string    // "app", "formula", "cask" or "npm"
	Version   string    // "" if unknown
	BundleID  string    // "" unless it is an app with a bundle identifier
	Source    string    // the source that found it
	Installed time.Time // zero if unknown
	Evidence  string    // where it was seen, for display
}

// Env is what the sources may use. Every field is injected so tests run on
// fixtures and never touch the real machine.
type Env struct {
	Home     string
	AppDirs  []string // where apps are looked for, e.g. /Applications, ~/Applications
	BinDirs  []string // where programs are looked for: the folders on PATH
	JavaDirs []string // where Java development kits are looked for
	Runner   platform.Runner
	HasTool  func(name string) bool // whether a native tool is installed
	Now      time.Time
}

// Source finds one kind of software. Discover must be read-only and must
// respect ctx.
type Source interface {
	Name() string
	Discover(ctx context.Context, env Env) ([]Software, error)
}

// Status says how one source did in this run.
type Status struct {
	Source string
	State  string // "ok", "unknown" (timed out or failed) or "absent" (its tool is not installed)
	Reason string // "" when ok
}

var (
	regMu   sync.RWMutex
	sources = map[string]Source{}
)

// Register adds a source. It panics on a duplicate name; call it from init().
func Register(s Source) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := sources[s.Name()]; dup {
		panic("inventory: duplicate source " + s.Name())
	}
	sources[s.Name()] = s
}

// Sources returns the registered sources, ordered by name.
func Sources() []Source {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Source, 0, len(sources))
	for _, s := range sources {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Result is everything one discovery run found, and how each source did.
type Result struct {
	Software []Software
	Statuses []Status
}

// Discover runs every registered source, each with its own Budget, and
// returns what they found, ordered by name then ID. A failing or slow source
// never stops the others.
func Discover(ctx context.Context, env Env) Result {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		res Result
	)
	for _, s := range Sources() {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			sctx, cancel := context.WithTimeout(ctx, Budget)
			defer cancel()
			found, err := s.Discover(sctx, env)
			st := Status{Source: s.Name(), State: "ok"}
			switch {
			case err != nil && isAbsent(err):
				st.State, st.Reason = "absent", err.Error()
			case err != nil:
				st.State, st.Reason = "unknown", err.Error()
			}
			mu.Lock()
			defer mu.Unlock()
			res.Software = append(res.Software, found...)
			res.Statuses = append(res.Statuses, st)
		}(s)
	}
	wg.Wait()
	sort.Slice(res.Statuses, func(i, j int) bool { return res.Statuses[i].Source < res.Statuses[j].Source })
	sort.Slice(res.Software, func(i, j int) bool {
		if res.Software[i].Source != res.Software[j].Source {
			return res.Software[i].Source < res.Software[j].Source
		}
		return res.Software[i].ID < res.Software[j].ID
	})
	return res
}

// errAbsent marks a source whose tool is not installed. That is a normal
// state, not a failure.
type errAbsent struct{ tool string }

func (e errAbsent) Error() string { return fmt.Sprintf("%s is not installed", e.tool) }

func isAbsent(err error) bool {
	var a errAbsent
	return errors.As(err, &a)
}

// Category groups the kinds of software for the summaries. Each category is
// one way software reaches the Mac, so it reads as one group.
func Category(kind string) string {
	switch kind {
	case "app", "formula", "cask", "package":
		return "apps and system packages"
	case "npm", "pip", "pipx", "uv-tool", "gem", "crate":
		return "language packages"
	case "runtime":
		return "runtimes"
	case "tool":
		return "programs on PATH"
	case "image", "model":
		return "images and models"
	}
	return "Other"
}

// CategoryOrder is the order the categories are listed in.
var CategoryOrder = []string{"apps and system packages", "language packages", "runtimes", "programs on PATH", "images and models", "Other"}

// PackageSources are the sources whose entries are package-manager installs.
// A PATH program that one of them already lists is not counted twice.
var PackageSources = []string{"brew", "npm", "pip", "pipx", "uv-tool", "gem", "cargo"}
