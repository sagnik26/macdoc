package recipes

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"macdoc/internal/model"
)

func init() {
	Register("brew_cleanup", detectBrewCleanup)
	Register("brew_autoremove", detectBrewAutoremove)
}

// brewTimeout bounds brew's dry-runs, which can be slow on large Cellars.
const brewTimeout = 60 * time.Second

// brewFreeRe matches brew cleanup's summary line, e.g.
// "==> This operation would free approximately 377MB of disk space."
var brewFreeRe = regexp.MustCompile(`would free approximately ([0-9.,]+\s*[KMGT]?B) of disk space`)

// detectBrewCleanup runs `brew cleanup -n --prune=all` and reports how much
// brew says it would free (SourceTool) and how many items it would remove.
func detectBrewCleanup(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Runner == nil || !ToolInstalled("brew") {
		return nil, nil
	}
	res, err := runDryRun(ctx, env, r, brewTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("brew cleanup -n: exit %d: %s", res.ExitCode, firstLine(res.Stderr))
	}
	out := string(res.Stdout) + "\n" + string(res.Stderr)
	items := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Would remove:") {
			items++
		}
	}
	if items == 0 {
		return nil, nil
	}
	f := Finding{
		Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "brew didn't report a total size"},
		Note:  plural(items, "item", "items") + " (old versions, downloads and logs)",
	}
	// Homebrew's disk_usage_readable uses binary units.
	if m := brewFreeRe.FindStringSubmatch(out); m != nil {
		if b, ok := parseSize(m[1], 1024); ok {
			f.Bytes = model.Bytes{Value: b, Source: model.SourceTool}
		} else {
			f.Bytes.Reason = fmt.Sprintf("brew reported an unreadable or implausible size %q", m[1])
		}
	}
	return []Finding{f}, nil
}

// detectBrewAutoremove runs `brew autoremove -n` and lists the formulae it
// would uninstall. brew doesn't report sizes, so Bytes is unknown.
//
// Assumed output shape (testdata/w2_brew_autoremove_n.txt):
//
//	==> Would autoremove 2 unneeded formulae:
//	libfoo
//	libbar
func detectBrewAutoremove(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Runner == nil || !ToolInstalled("brew") {
		return nil, nil
	}
	res, err := runDryRun(ctx, env, r, brewTimeout)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("brew autoremove -n: exit %d: %s", res.ExitCode, firstLine(res.Stderr))
	}
	var formulae []string
	inList := false
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "==>"):
			inList = strings.Contains(line, "Would autoremove") || strings.Contains(line, "Would uninstall")
		case inList && line != "" && !strings.HasPrefix(line, "Warning"):
			formulae = append(formulae, line)
		}
	}
	if len(formulae) == 0 {
		return nil, nil
	}
	return []Finding{{
		Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "brew autoremove doesn't report sizes"},
		Note:  "Would uninstall " + plural(len(formulae), "formula", "formulae") + ": " + strings.Join(formulae, ", "),
	}}, nil
}
