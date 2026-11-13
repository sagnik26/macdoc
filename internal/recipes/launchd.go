package recipes

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

func init() { Register("broken_launch_agents", DetectBrokenLaunchAgents) }

// maxAgents bounds how many property lists one run reads.
const maxAgents = 500

// DetectBrokenLaunchAgents emits the user background items in
// params["dir"] (~/Library/LaunchAgents) whose program no longer exists: the
// property list names an absolute path that is gone, which is what an app
// leaves behind when it is deleted without its helper being unloaded. Each
// finding carries the plist (Target) and its Label (NativeArg), which the
// unload recipe passes to launchctl remove. Apple's items (com.apple.*), items
// whose program still exists, and items with no absolute program path are
// skipped. Anything that can't be read is skipped, never guessed.
func DetectBrokenLaunchAgents(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	dirParam, err := RequireParam(r, "dir")
	if err != nil || env.Runner == nil {
		return nil, err
	}
	dir := ExpandHome(env.Home, dirParam)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil //nolint:nilerr // no folder means no background items
	}
	var out []Finding
	for i, e := range entries {
		if i >= maxAgents || e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		plist := filepath.Join(dir, e.Name())
		label, program, ok := readAgent(ctx, env.Runner, plist)
		if !ok || strings.HasPrefix(strings.ToLower(label), "com.apple.") || CheckNativeArg(label) != nil {
			continue
		}
		if !filepath.IsAbs(program) {
			continue
		}
		if _, err := os.Lstat(program); !os.IsNotExist(err) {
			continue
		}
		out = append(out, Finding{
			Target: plist, NativeArg: label, Title: label + " (its program is gone)",
			Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "a background item is a tiny file"},
			Note:  "The program " + program + " no longer exists.",
		})
	}
	return out, nil
}

// readAgent reads a launchd property list as JSON through plutil and returns
// its Label and the program it starts.
func readAgent(ctx context.Context, r platform.Runner, plist string) (label, program string, ok bool) {
	res, err := r.Run(ctx, platform.Command{Name: "plutil", Args: []string{"-convert", "json", "-o", "-", plist}})
	if err != nil || res.ExitCode != 0 {
		return "", "", false
	}
	var m struct {
		Label            string   `json:"Label"`
		Program          string   `json:"Program"`
		ProgramArguments []string `json:"ProgramArguments"`
	}
	if json.Unmarshal(res.Stdout, &m) != nil || m.Label == "" {
		return "", "", false
	}
	program = m.Program
	if program == "" && len(m.ProgramArguments) > 0 {
		program = m.ProgramArguments[0]
	}
	return m.Label, program, program != ""
}
