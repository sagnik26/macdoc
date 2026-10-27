package cli

import (
	"os"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

// termSize reports the terminal size of f.
func termSize(f *os.File) (w, h int, err error) { return term.GetSize(f.Fd()) }

// truncateANSI cuts s to n visible cells, keeping escape sequences intact.
func truncateANSI(s string, n int) string { return ansi.Truncate(s, n, "…") }

// wrapANSI soft-wraps each line to w cells, keeping escape sequences.
func wrapANSI(s string, w int) string { return ansi.Wrap(s, w, "") }
