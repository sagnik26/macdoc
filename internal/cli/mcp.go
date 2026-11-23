package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/mcp"
)

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server for Claude (it can read and suggest only; it can't plan, apply or delete)",
		Long: `macdoc mcp runs macdoc's MCP server on stdin/stdout. Claude Code or Claude
Desktop starts it; you don't run it yourself.

  claude mcp add macdoc -- macdoc mcp          # Claude Code
  Claude Desktop: in claude_desktop_config.json, under "mcpServers":
    "macdoc": {"command": "/full/path/to/macdoc", "args": ["mcp"]}

Claude can read the diagnosis and suggest what to clean. It can't plan, apply,
delete, move or restore anything, pass a path, run a command or change
settings. To clean something, run macdoc reclaim in Terminal.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			cat, err := LoadCatalog()
			if err != nil {
				return err
			}
			if IsTerminal(cmd.InOrStdin()) {
				// stdout is the protocol; a hint for a person goes to stderr.
				fmt.Fprintln(os.Stderr, "macdoc mcp is the MCP server for Claude and waits for a client on stdin. See: macdoc mcp --help. Ctrl+C quits.")
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return mcp.Run(ctx, mcp.Options{Home: home, Runner: Runner, Catalog: cat, Version: version, ScanRoot: ScanRoot})
		},
	}
}
