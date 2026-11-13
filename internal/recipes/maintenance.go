package recipes

import "context"

func init() { Register("maintenance", detectMaintenance) }

// detectMaintenance emits one finding for a maintenance recipe when its tool
// is installed. The step frees no measurable space, so the size is unknown
// and never shown as zero.
func detectMaintenance(_ context.Context, _ Env, r Recipe) ([]Finding, error) {
	if r.Native == nil || len(r.Native.Argv) == 0 || !ToolInstalled(r.Native.Argv[0]) {
		return nil, nil
	}
	return []Finding{{Title: r.Title}}, nil
}
