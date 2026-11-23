# macdoc UI migration plan: Charm v2 stack and Bubbles components

Goal: move macdoc's terminal UI from Bubble Tea / Bubbles / Lip Gloss v1 to
v2, and replace hand-built interactive components with Bubbles v2
components, styled per `docs/DESIGN_SYSTEM.md`.

Rules for every phase:
- One phase per branch and PR. Do not combine phases.
- Safety behavior does not change: plan, confirm, apply, audit log, typed
  codes, advice-only items. No change to `internal/executor`.
- After each phase: `make test`, `make lint`, `make importcheck`,
  `make forbidcheck`, `make check` pass, and `macdoc` runs.
- Capture before and after (terminal screenshots, and plain-text output
  diffs) and put them in the PR.
- If a v2 symbol is not described in the Charm v2 upgrade guides, check it with
  `go doc` before using it. Do not guess.
- Dependencies: the owner approved moving the Charm modules to their v2 `charm.land`
  paths (2026-11-24). Nothing else is added.
- Commits: none are made without the owner asking; the work is verified in the
  working tree phase by phase.

Current state (from `go.mod`): bubbles v1.0.0, bubbletea v1.3.10, lipgloss
v1.1.0, termenv v0.16.0, x/ansi v0.11.9, x/term v0.2.2. Files that draw output
live in `internal/cli`; the largest are `dashboard.go`, `tables.go`,
`reclaimflow.go`, `software.go`, `theme.go`.

## Phase 0: baseline (done)

1. Branch `ui/charm-v2`.
2. Golden snapshots in `internal/cli/tests/testdata/golden`, written by
   `go test ./internal/cli/tests -run TestGolden -update-golden`:
   - 42 dashboard views (`TestGoldenDashboardTabs`, `TestGoldenDashboardPages`):
     the five scan tabs at 100 and 140 columns, and the welcome screen, help,
     vitals and retire pages at 100, each in the plain layout, and in color on a
     dark and a light background (`cli.ForcedColorTheme`). The plain files hold
     no escape sequence; the color files do.
   - 6 printed reports in plain mode (`TestGoldenPrintedReports`): reclaim,
     reclaim --all, the plan dry run, retire, vitals and the overview.
   The commands the original plan listed (`doctor`, `wins`, `plan`, `apply`,
   `health`, `space`, `projects`) no longer exist; today's are `scan`, `reclaim`,
   `retire`, `explore`, `vitals`, `log` and `quarantine`.
3. Real terminal screenshots (light and dark, about 100 and 140 columns) are a
   manual step: they cannot be taken from the test run. Capture them once before
   Phase 2 and attach them to that PR.
4. Binary size before the migration: 16,203,490 bytes (about 16.2 MB, `make build`).
5. `docs/DESIGN_SYSTEM.md` is now the macdoc terminal spec; the earlier file
   described an unrelated web app.
Exit: baseline artifacts exist; `make check` is green.

## Phase 1: dependency and compile (mechanical, no visual change)

1. `go get charm.land/bubbletea/v2@latest charm.land/bubbles/v2@latest charm.land/lipgloss/v2@latest`.
   Replace import paths in all files. Remove `muesli/termenv` if unused.
2. Rewrite `theme.go`:
   - Styles become plain `lipgloss.NewStyle()` values; delete the
     `*lipgloss.Renderer` field and `termenv` use.
   - Replace `pc(...)` / `CompleteAdaptiveColor` with a function of `isDark`
     using `lipgloss.LightDark(isDark)`; keep the existing hex values for now,
     so nothing changes visually.
   - Replace `lipgloss.TerminalColor` with `image/color.Color`.
   - Background: `lipgloss.HasDarkBackground(os.Stdin, os.Stdout)` for printed
     output, honoring `MACDOC_THEME`.
3. **Plain mode (highest risk).** v1 stripped all ANSI when the profile was
   ASCII. v2's `Render` always emits ANSI. Build `PlainTheme()` from styles
   with no color and no attributes (no `Bold`, no `Underline`), and keep the
   "decorations only when color is on" rule. Printed output goes through
   `lipgloss.Println` / a `colorprofile` writer so non-terminals are
   downsampled or stripped.
4. Bubble Tea: `View() string` becomes `View() tea.View`; `tea.KeyMsg`
   becomes `tea.KeyPressMsg`; `" "` becomes `"space"`; mouse messages split
   by type; remove `tea.WithAltScreen()`, `tea.WithMouseCellMotion()`,
   `tea.EnterAltScreen` and similar in favor of `View` fields; `p.Start()`
   becomes `p.Run()`; `tea.Sequentially` becomes `tea.Sequence`.
5. Request the background color in `Init` (`tea.RequestBackgroundColor`) and
   rebuild styles on `tea.BackgroundColorMsg`.
6. Move any Bubbles field access to the v2 methods (`SetWidth`, `Height()`,
   `SetYOffset`, and so on).
Exit: plain golden output is byte-identical to Phase 0; screenshots match
within terminal rendering noise; tests pass.

## Phase 2: Charm palette for chrome

1. Replace the coral brand and cyan accent with the chrome tokens in
   `DESIGN_SYSTEM.md` section 3.1 (brand `#7571F9`, gradient
   `#5A56E0` to `#EE6FF8`, hint and muted neutrals, selection ANSI 212, title
   chip ANSI 62 on ANSI 230).
2. Keep the semantic colors (section 3.3) exactly.
3. Retire cyan, violet and the old text/muted/faint values; update the JSON
   view mapping.
4. Contrast check in light and dark: text on `selectBg`, on badges, and
   muted text on both backgrounds.
Exit: new screenshots reviewed side by side with Phase 0; plain output still
byte-identical.

## Phase 3: component swaps (one component per PR, in this order)

Already on Bubbles v1 today, so they only need the v2 change from Phase 1:
`help`, `key`, `progress`, `spinner` and `textinput`. Items 1, 2 and 4 below
reduce to verifying them under v2. `/reclaim` in the dashboard no longer shows a
picker (it starts at once), so item 7 applies only to the terminal command
`macdoc reclaim`.

| Order | Replace | With | Done when |
|---|---|---|---|
| 1 | custom footer / help text | `help` + `key` with per-view `KeyMap` | footer matches current keys; `?` overlay lists every key and `/` command |
| 2 | scan progress line | `progress` (default blend) + `spinner` | bytes, files, current folder and % of last scan still shown; `esc` still stops; plain mode prints a static line |
| 3 | detail pane and explain text | `viewport` | scrolls; wraps with `Theme.Wrap`; 120-column side-by-side and narrow `e`/`esc` behavior unchanged |
| 4 | `/` command line | `textinput` | completion (`tab` completes, arrows pick) unchanged |
| 5 | scan age, plan expiry | `stopwatch` / `timer` where a live count helps | no permanent timers when idle |
| 6 | Suggestions and other interactive tables | `table` (see `DESIGN_SYSTEM.md` 4.1) | IDs, sizes, risk labels never truncate; sizes right-aligned; rounded border; `▶` marker; marking with `space`; plans and removal flow unchanged |
| 7 | tier picker, reclaim picker | `list` | selection and confirmation flows unchanged |

Phase 3.6 is the largest item. Do it last, behind the same keybindings, and
compare against Phase 0 at every width. If the Bubbles table cannot meet the
"never truncate IDs, sizes, risk" rule without large glue code, keep the
current table implementation and restyle it with the new tokens, and record
that in `docs/adr/`.

Printed (non-TUI) reports keep their own Lip Gloss rendering. Bubbles
components do not run there.

## Phase 4: finish

1. Update README screenshots and the "Colors" paragraph (palette, `NO_COLOR`,
   `MACDOC_THEME`).
2. Update `docs/CLI_UX.md` if it describes colors or key hints.
3. Add an ADR: why v2, the plain-mode approach, and the table decision.
4. Run `make vulncheck` and a full `make check`; compare binary size.
5. Update `go.mod` dependabot groups for the new `charm.land` modules.

## Risks to watch

1. **Plain output regression.** Highest risk; guard with the golden files.
2. **Selected-row styling with colored cells.** ANSI resets can cancel a row
   background.
3. **Vanity import paths.** `charm.land/...` modules must be reachable in CI
   and for reproducible release builds; confirm the release pipeline builds.
4. **Terminal support.** Key and background-color features depend on the
   terminal. Test macOS Terminal, iTerm2 and at least one more.
5. **Scope.** This touches most of `internal/cli`. If a phase grows, split it.

## Out of scope

A reference design (none is used), new features, telemetry, and any change to the
safety model.
