package inventory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sagnik26/macdoc/internal/platform"
)

func init() {
	Register(brewSource{})
	Register(npmSource{})
}

// The commands below are the whole of what the package managers are asked.
// They list; they never install, upgrade or clean (ADR 0003).

// brewSource lists Homebrew formulae and casks.
type brewSource struct{}

// Name implements Source.
func (brewSource) Name() string { return "brew" }

// Discover implements Source.
func (brewSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if env.HasTool != nil && !env.HasTool("brew") {
		return nil, errAbsent{"brew"}
	}
	var out []Software
	for _, q := range []struct{ flag, kind string }{{"--formula", "formula"}, {"--cask", "cask"}} {
		names, err := listLines(ctx, env, "brew", "list", q.flag)
		if err != nil {
			return out, err
		}
		for _, n := range names {
			out = append(out, Software{
				ID: "brew:" + n, Name: n, Kind: q.kind, Source: "brew",
				Evidence: "brew list " + q.flag,
			})
		}
	}
	return out, nil
}

// npmSource lists globally installed npm packages.
type npmSource struct{}

// Name implements Source.
func (npmSource) Name() string { return "npm" }

// Discover implements Source.
func (npmSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if env.HasTool != nil && !env.HasTool("npm") {
		return nil, errAbsent{"npm"}
	}
	if env.Runner == nil {
		return nil, errAbsent{"npm"}
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: "npm", Args: []string{"ls", "-g", "--depth=0", "--json"}})
	if err != nil {
		return nil, err
	}
	// npm exits 1 when a peer dependency is missing but still prints the list.
	var doc struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(res.Stdout, &doc); err != nil {
		return nil, fmt.Errorf("npm ls: %w", err)
	}
	var out []Software
	for name, dep := range doc.Dependencies {
		out = append(out, Software{
			ID: "npm:" + name, Name: name, Kind: "npm", Version: dep.Version, Source: "npm",
			Evidence: "npm ls -g",
		})
	}
	return out, nil
}

// listLines runs a read-only command and returns its non-empty output lines.
func listLines(ctx context.Context, env Env, name string, args ...string) ([]string, error) {
	if env.Runner == nil {
		return nil, errAbsent{name}
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: name, Args: args})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("%s %v exited %d", name, args, res.ExitCode)
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(res.Stdout))
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

// hasTool reports whether a native tool is installed. Without a checker, the
// tool is assumed present (tests inject one).
func hasTool(env Env, name string) bool {
	return env.HasTool == nil || env.HasTool(name)
}

// output runs a read-only command and returns its standard output. A non-zero
// exit is an error.
func output(ctx context.Context, env Env, name string, args ...string) ([]byte, error) {
	if env.Runner == nil {
		return nil, errAbsent{name}
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: name, Args: args})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		// The first line of the error says why (for example "no default
		// toolchain"), so the status can show it.
		why := firstLine(res.Stderr)
		if why == "" {
			why = firstLine(res.Stdout)
		}
		if why == "" {
			why = fmt.Sprintf("exited %d", res.ExitCode)
		}
		return nil, fmt.Errorf("%s: %s", name, why)
	}
	return res.Stdout, nil
}

// lines splits output into its non-empty lines.
func lines(b []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if l := strings.TrimRight(sc.Text(), " \t\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// firstLine is the first non-empty line of b, trimmed.
func firstLine(b []byte) string {
	for _, l := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}
