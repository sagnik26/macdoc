# Read-only macdoc demo: welcome screen, reclaim list, vitals.
# Render on a Mac, where macdoc runs: `make demo` (needs https://github.com/charmbracelet/vhs).
# Writes docs/demo.gif next to this file.
#
# The tape never confirms a cleanup. `reclaim --dry-run` only lists, including
# at a real terminal, so Enter here does not start a removal.

Output docs/demo.gif

Require macdoc

Set Shell bash
Set FontSize 16
Set Width 1200
Set Height 720
Set Padding 20
Set Framerate 30
Set Theme "Catppuccin Mocha"

Hide
Type "clear"
Enter
Show

# Welcome screen. q quits; nothing is scanned or removed.
Type "macdoc"
Enter
Sleep 2s
Type "q"

Sleep 1s

# Suggestion list. --dry-run does not ask which risk types to clean.
Type "macdoc reclaim --dry-run"
Enter
Sleep 3s

Type "macdoc vitals"
Enter
Sleep 3s
