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
		Short: "Run the MCP server for Claude (read and propose plans only; it can't apply anything)",
		Long: `macdoc mcp runs macdoc's MCP server on stdin/stdout. Claude Code or Claude
Desktop starts it; you don't run it yourself.

  claude mcp add macdoc -- macdoc mcp          # Claude Code
  Claude Desktop: in claude_desktop_config.json, under "mcpServers":
    "macdoc": {"command": "/full/path/to/macdoc", "args": ["mcp"]}

Claude can read the diagnosis and propose plans. It can't apply a plan, delete,
move or restore anything, pass a path, run a command or change settings. To
carry out a plan Claude proposed, run the macdoc apply command it gives you in
Terminal and confirm each item.`,
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
