# DESIGN_SYSTEM.md: macdoc terminal UI

How macdoc looks in a terminal: the dashboard, the printed reports and the
prompts. It replaces an earlier file that described a different product (a web
app). `docs/UI_MIGRATION_PLAN.md` says how the code gets there.

**What is decided and what is derived.** The brand color, the gradient, the
selection color and the title chip come from the migration plan. The neutrals
and the light-background values are derived from the current theme
(`internal/cli/theme.go`) and are a starting point: adjust by eye against the
golden files in `internal/cli/tests/testdata/golden`.

## 1. Principles

- **Plain first.** With no color (a pipe, a file, `NO_COLOR`, `TERM=dumb`, a test)
  every style renders as the bare text. Decorations such as cards, badges and
  rules appear only when color is on. Plain output never contains an escape
  sequence.
- **Information first.** Tables and totals read at a glance. Sizes line up on the
  right. IDs, sizes and risk labels are never cut off.
- **Safety is visible.** Risk is shown by tier name and color together, never by
  color alone. Removal states what it will do and what it cannot undo.
- **Quiet.** One accent family, no emoji, no decoration beyond the elements below.
- **Honest numbers.** A size is measured, tool-reported, estimated or unknown;
  unknown is shown as unknown, never as zero.

## 2. Plain mode

| Situation | Result |
|---|---|
| Output is not a terminal, `NO_COLOR` is set, or `TERM=dumb` | Plain: bare text, no escape sequences |
| `--quiet` or `--json` | No decoration; JSON is never styled |
| The dashboard with no color support | Plain layout: boxes and badges drawn without color |
| `MACDOC_THEME=light\|dark` | Forces the background assumption; otherwise it is detected from the terminal |

Rules: a plain style sets no foreground, no background and no attributes (no bold,
no underline). Anything a person must be able to read without color (risk names,
`✓`, `▶`, the marked count) carries its own text.

## 3. Color

Each color has a dark-background and a light-background value, and degrades to
256 and 16 colors.

### 3.1 Chrome tokens (the migration changes these)

| Token | Dark | Light | Used for |
|---|---|---|---|
| `brand` | `#7571F9` | `#5A56E0` | The `◆ macdoc` mark, headings, the active tab |
| `gradient` | `#5A56E0` to `#EE6FF8` | same | The header rule and the welcome banner |
| `accent` | `#7571F9` | `#5A56E0` | IDs, links, command names |
| `select` (foreground) | ANSI 212 | ANSI 162 | The selected row marker `▶` and its text |
| `titleChip` | ANSI 62 on ANSI 230 | ANSI 62 on ANSI 230 | The page title chip |
| `selectBg` | `#2F3340` | `#E3E7F2` | The selected row background (kept from today) |

### 3.2 Text neutrals (kept as they are)

| Token | Dark | Light |
|---|---|---|
| `text` | `#ECECEC` | `#1C1C1C` |
| `muted` | `#9A9A9A` | `#6C6C6C` |
| `faint` | `#4E4E4E` | `#C6C6C6` |
| `onBadge` | `#141414` | `#FFFFFF` |

### 3.3 Semantic colors
These carry meaning, so each stays a distinct hue, and risk is always written out
as well as colored. They are the neon family of the Bubbles README (its mint
wordmark, soft yellow and the cat's coral), deeper on a light background so they
read on white.

| Meaning | Dark | Light | Where |
|---|---|---|---|
| SAFE, ok, done | `#5CFFD0` | `#0B8F6A` | SAFE tier, success lines |
| CAUTION, warning | `#FDFF8C` | `#8A6D00` | CAUTION tier, warnings |
| RISKY | `#FF9A5C` | `#C2570C` | RISKY tier |
| Bad, error, critical | `#FF6B6B` | `#C62828` | Failures, the memory alert box |
| Info | `#8FB3FF` | `#2457C5` | Neutral notices |

### 3.4 JSON view

Keys use `accent`, strings the SAFE mint, numbers the CAUTION yellow and
literals the info blue. The old violet and cyan are retired.

### 3.5 Contrast

Text on `selectBg`, text on a badge, and `muted` text must stay readable on both
backgrounds. Check each palette change in a light and a dark terminal.

## 4. Components

### 4.1 Table
- Rounded border, one header row, no row separators.
- Columns: the ID and the risk label never truncate; the title wraps or ellipsizes
  first; sizes are right-aligned and use one unit style (`1.2 GB`, `640 MB`).
- The selected row shows `▶` and `selectBg`. A marked row shows `✓`.
- A table that does not fit shows fewer rows and says "showing N of M", never
  clips a column.

### 4.2 Header card and tabs
- The `◆ macdoc` mark, the section name, the scan ID and the scan age.
- Tabs are numbered `1`–`5`; the active tab uses a `brand` background chip.

### 4.3 Risk badges
- `SAFE`, `CAUTION`, `RISKY` and `ADVICE` in their semantic color, always with
  the word. Section headers repeat the tier with its total.

### 4.4 Progress bar and spinner
These are the Bubbles README components, unchanged (`charm.land/bubbles/v2`).
- **Bar:** `progress.New(progress.WithDefaultBlend())` in the Bubbles README's colors,
  drawn in half height: the fill and the track are lower half blocks `▄`, the fill
  blended from `#5A56E0` to `#EE6FF8` (the blend spans the whole bar, so a half-full
  bar shows only the first half of it), the track dim `#606060`, and the percentage
  right after the bar (` 25%`). Up to 80 cells wide. A blank line separates the bar from
  the text under it.
- **Metric bars** (the CPU, memory, disk and power bars on the Vitals page) keep their
  full height: `█` cells on the same blend over a dim `░` track, with the percentage
  after the bar. Only the loading bars are half height.
- **Hint text** under a bar is `#626262`, as in the README example ("Press any key to quit").
- **Unknown total:** the same bar sweeps back and forth with no percentage, and the
  hint says the size is unknown. One bar is shown for every scan, retire read and
  vitals reading, on the dashboard and on the command line (where it is written
  through `colorprofile`, so 256- and 16-color terminals and `NO_COLOR` still work).
- **Spinner:** `spinner.Dot` (braille dots) in ANSI 205, beside the step in progress;
  finished steps show `✓` and their time. No other animation.

### 4.5 Footer help
- `help.DefaultStyles` from Bubbles: dim gray keys, descriptions and the ` • `
  separator, as in the README's "? toggle help • q quit" line. One line of
  `key  action` pairs for the current view; it drops the least useful hints
  first and always keeps `? help`, `esc home` and `q quit`. `?` opens the full list.

### 4.6 Command line (`/`)
- A Bubbles text input with its default styles (block cursor), with a list of
  completions below it; `tab` completes, arrows pick, `esc` leaves. The list is
  exactly the commands on the welcome screen.

### 4.7 Boxes
- **Alert box:** a red rounded box with a bold title, for things that need attention
  now.
- **Panel:** a rounded `accent` border around the part of a page a person acts on.
- **Removal box:** lists each item with its risk, size and path, the total, and
  what Enter and `esc` do; a RISKY item asks for its typed ID.

### 4.9 Welcome lettering
- The welcome screen's lettering is "macdoc" in the six-row block (figlet "ANSI Shadow")
  style, on the brand gradient in color and plain without it. There is no mascot. A small
  terminal shows a compact `◆ macdoc` title instead.
- The welcome text is airier when there is room: two blank lines between its blocks and
  padding inside the command box. A terminal app cannot change the size of ordinary text;
  that is the terminal's font size.

### 4.8 Detail pane and overlays
- Scrollable, wraps to the width, `esc` closes. Overlays replace the page; they do
  not stack.

## 5. Layout

- Designed for 80 columns and up; the dashboard side-by-side detail needs 120.
- Below 104 columns a menu drops its descriptions and shows only the selected one.
- Printed reports wrap to the terminal width; plain reports never wrap tables.
- One blank line between sections; no trailing spaces.

## 6. Motion

Only the spinner and the progress bar move. Nothing blinks, scrolls by itself or
redraws when idle (the live vitals page is the one place that redraws, once every
three seconds).

## 7. Things we are not doing

- No emoji and no icon fonts.
- No color as the only carrier of meaning.
- No decoration in plain mode.
- No themes beyond light and dark.
- No change to what a screen means or to the safety model for the sake of looks.
