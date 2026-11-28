# 0016: The terminal UI moves to Charm v2, gets its own palette, and keeps its own table and tier picker

**Status:** Accepted (owner decision, 2026-11-24: "complete the whole plan"). Implemented. **Recorded in:** `go.mod`, `internal/cli/theme.go`, `internal/cli/dashboard.go`, `docs/DESIGN_SYSTEM.md`, `docs/UI_MIGRATION_PLAN.md`.

## Context

The dashboard and the printed reports used Bubble Tea 1.3, Bubbles 1.0 and Lip Gloss 1.1, with colors tied to a Lip Gloss `Renderer` and `termenv`. The owner asked for a full UI/UX revamp following `docs/UI_MIGRATION_PLAN.md`: move to the v2 stack, restyle with a macdoc palette, and replace hand-built components with Bubbles ones where they fit. `AGENTS.md` asks to be consulted before adding a dependency; the owner approved the v2 modules (`charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`), which replace the old ones. `termenv` is gone; `colorprofile` (already a transitive dependency) is now used directly.

## Decision

1. **Move to the v2 modules.** `View()` returns a `tea.View` (alternate screen is a view field), keys are `KeyPressMsg` (space is `"space"`), and the program is started without options. The dashboard keeps a `Screen() string` method that the tests and the snapshots read.
2. **Plain mode is a theme, not a renderer.** v2's `Render` always emits escape codes, so the old "ascii profile strips everything" no longer holds. `PlainTheme()` is built from styles with no color and no attributes, a theme that draws no color (`color == false`) never emits one, and `Screen()` strips escapes as a last guard for the plain layout. Printed output reduces colors to the terminal's depth inside the theme (`Theme.c` through `colorprofile`), so no writer wrapper is needed and `RealTTY` still sees the real file.
3. **The terminal's background is asked for.** A colored dashboard requests the background color at start and restyles if the answer differs from the guess made before the program started; `MACDOC_THEME` overrides it.
4. **The look is `docs/DESIGN_SYSTEM.md` section 3.** Indigo brand, a brand-to-pink gradient, ANSI 212/162 for the selection marker, ANSI 62 on 230 for the page title chip. The neutrals and the semantic colors (green, yellow, orange, red, blue) are unchanged, and risk is always written out as well as colored. `TestPaletteContrast` checks readability on a dark and a light background.
5. **Components.** Already Bubbles and now on v2: `help`, `key`, `spinner`, `progress`, `textinput`. Newly adopted: `viewport` (the scroll offset of every text body). Not adopted, on purpose:
   - **`table`.** The Suggestions table needs a colored cell per risk, never-truncated ID, size and risk columns, and a selected row whose background survives those colors. Bubbles' table styles whole rows, not cells, and cannot fit columns around the ID rule without most of today's code. The current `lipgloss/table` grid and its column fitting stay, restyled with the new tokens.
   - **`list` for the tier picker.** `macdoc reclaim` shows three checkbox rows and a separate Clean button, and only Enter on that button starts a cleanup. A list brings filtering, pagination and its own key handling that this form does not want, and the confirmation flow is a safety property (ADR 0013). `TierPicker` stays, restyled.
   - **`stopwatch` and `timer`.** Nothing needs a live count that the spinner tick does not already redraw, and the plan says no permanent timers when idle.
6. **The footer fits.** A footer that is wider than the terminal drops hints from the middle and always keeps `? help`, `esc home` and `q quit`.

## Consequences

- The dependency set is the Charm v2 modules plus `colorprofile`; `termenv` is removed. Dependabot groups `charm.land/*` with the older Charm modules.
- On 16- and 256-color terminals the colors are the nearest of the palette, not hand-picked ANSI values as before.
- Two visible changes came with the move, both fixes: the help overlay's "Safety" text, which was garbled in the old color and plain-layout render, now reads correctly, and the Suggestions footer is no longer longer than the terminal.
- A real-terminal check (macOS Terminal, iTerm2 and one more, light and dark) is still a manual step; the snapshots cover the rendering, not each terminal's color support.

Tests: `internal/cli/tests.TestGoldenDashboardTabs`, `internal/cli/tests.TestGoldenDashboardPages`, `internal/cli/tests.TestGoldenPrintedReports`, `internal/cli/tests.TestPaletteContrast`, `internal/cli/tests.TestBackgroundColorRestylesTheDashboard`, `internal/cli/tests.TestTUIFooterFitsAndKeepsHelpAndQuit`, `internal/cli/tests.TestThemeDegrades`.
