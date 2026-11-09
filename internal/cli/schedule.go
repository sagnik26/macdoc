package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/store"
)

// ScheduleResult is what `macdoc schedule` did and what to run next.
type ScheduleResult struct {
	Action string `json:"action"`         // enable or disable
	Label  string `json:"label"`          // the launch agent's name
	Path   string `json:"path"`           // the one file written or removed
	Done   bool   `json:"done"`           // whether that file changed
	Next   string `json:"next,omitempty"` // the launchctl line, for Terminal
}

// newScheduleCmd installs or removes the daily read-only scan. It writes and
// removes one LaunchAgents file; launchctl (which starts and stops a
// background job) is left to the person, in Terminal.
func newScheduleCmd(g *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Run a read-only scan every morning (a launchd agent)",
		Long: `macdoc schedule enable writes a launchd agent that runs macdoc scan --quiet
every morning at low priority. It only scans: it never removes anything.
macdoc schedule disable removes that file.

macdoc doesn't start or stop the agent itself, because launchctl changes
what runs in the background. Each command prints the launchctl line to run
in Terminal.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "enable",
		Short: "Write the daily scan agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if exe, err = filepath.EvalSymlinks(exe); err != nil {
				return err
			}
			logPath := filepath.Join(filepath.Dir(store.DefaultPath(home)), "scan.log")
			path, err := executor.InstallSchedule(home, exe, logPath)
			if err != nil {
				return err
			}
			res := ScheduleResult{Action: "enable", Label: executor.ScheduleLabel, Path: path, Done: true,
				Next: fmt.Sprintf("launchctl bootstrap gui/%d %s", os.Getuid(), path)}
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "schedule enable", res)
			}
			return writeText(cmd, fmt.Sprintf(
				"Daily read-only scan written: %s (every day at %02d:00).\nIt isn't running yet. Start it once in Terminal:\n  %s",
				path, executor.ScheduleHour, res.Next))
		},
	}, &cobra.Command{
		Use:   "disable",
		Short: "Remove the daily scan agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			removed, err := executor.RemoveSchedule(home)
			if err != nil {
				return err
			}
			path := executor.ScheduleAgentPath(home)
			res := ScheduleResult{Action: "disable", Label: executor.ScheduleLabel, Path: path, Done: removed,
				Next: fmt.Sprintf("launchctl bootout gui/%d/%s", os.Getuid(), executor.ScheduleLabel)}
			msg := "No daily scan was scheduled. Nothing to remove."
			if removed {
				msg = fmt.Sprintf("Daily scan removed: %s.\nIf it's still running, stop it in Terminal:\n  %s", path, res.Next)
			}
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "schedule disable", res)
			}
			return writeText(cmd, msg)
		},
	})
	return cmd
}

func writeText(cmd *cobra.Command, text string) error {
	_, err := fmt.Fprintln(cmd.OutOrStdout(), text)
	return err
}
