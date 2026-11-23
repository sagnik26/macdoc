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

### 3.3 Semantic colors (never change with the palette)

| Meaning | Dark | Light | Where |
|---|---|---|---|
| SAFE, ok, done | `#6BD68A` | `#1E8A4C` | SAFE tier, success lines |
| CAUTION, warning | `#F2CC60` | `#9A6D00` | CAUTION tier, warnings |
| RISKY | `#F4A261` | `#C2570C` | RISKY tier |
| Bad, error, critical | `#F26B6B` | `#C62828` | Failures, the memory alert box |
| Info | `#8FB3FF` | `#2457C5` | Neutral notices |

### 3.4 JSON view

Keys use `accent`, strings the SAFE green, numbers the CAUTION yellow and
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
- A bar is shown for every scan, retire read and vitals reading: a percentage when
  the total is known, a sweeping bar and "size unknown" when it is not.
- A spinner marks the step in progress; finished steps show `✓` and their time.
- No other animation.

### 4.5 Footer help
- One line of `key  action` pairs for the current view, separated by ` • `.
  `?` opens the full list.

### 4.6 Command line (`/`)
- A text input with a list of completions below it; `tab` completes, arrows pick,
  `esc` leaves. The list is exactly the commands on the welcome screen.

### 4.7 Boxes
- **Alert box:** a red rounded box with a bold title, for things that need attention
  now.
- **Panel:** a rounded `accent` border around the part of a page a person acts on.
- **Removal box:** lists each item with its risk, size and path, the total, and
  what Enter and `esc` do; a RISKY item asks for its typed ID.

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
