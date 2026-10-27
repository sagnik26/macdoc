# macdoc: CLI UX

## 1. Commands

> **Implemented structure (2026-10-27):** one command, `macdoc doctor` (also bare `macdoc`), shows the full overview. Detail views are its sections: `doctor health | space [--large] | wins [--all] [--tier] | explain <id> | dev [toolchain] | scan`. `macdoc doctor` is the short summary from the latest scan, and `doctor scan` rescans and prints the full report (every section). There is no `--scan` flag. The flat command list below is the original design. `plan`/`apply`/`quarantine`/`log`/`trend`/`slow` will be added as top-level commands (`plan`/`apply`) or doctor sections (`slow`, `trend`) when built.

| Command | Mode | Purpose |
|---|---|---|
| `macdoc` / `macdoc doctor` | read | One-screen health summary and top findings |
| `macdoc scan [--rescan] [--workers N] [--foreground]` | read | Run or refresh the full crawl. Other commands reuse a scan < 6 h old |
| `macdoc wins [--all] [--tier safe\|caution\|risky]` | read | Top space wins, tiered |
| `macdoc storage [--large]` | read | Category breakdown with "System Data" decoded. `--large` lists unknown big items |
| `macdoc dev [xcode\|android\|node\|docker\|python\|ai\|brew]` | read | Per-toolchain breakdown |
| `macdoc projects [--idle 90]` | read | Projects with idle days and artifact weight |
| `macdoc slow` | read | Memory pressure, swap and heavy processes, with an explanation |
| `macdoc explain <O-id>` | read | What it is, why it's there, restore cost, exact action |
| `macdoc trend [--days 30]` | read | Growth per category across scans |
| `macdoc plan <O-id…> \| --safe \| --recipe <id>` | writes plan file | Dry-run plan, sealed |
| `macdoc apply <P-id>` | **mutating, TTY** | Confirm, re-validate, execute, verify |
| `macdoc quarantine list\|restore <Q-id>\|purge [--expired]` | restore/purge mutating, TTY | Manage quarantine |
| `macdoc log [--json]` | read | Audit history |
| `macdoc config [get\|set\|edit]` | config | Thresholds, protected paths, expiry |
| `macdoc mcp` | read + plans (Phase C) | MCP stdio server |
| `macdoc schedule enable\|disable` | Phase 10 | Daily read-only scan via launchd |

Global flags: `--json`, `--quiet`. (`--no-color` and `--verbose` were dropped as unused. Color follows the `NO_COLOR` convention instead.)

### 1a. The dashboard and colors (implemented 2026-10-28)

- **Bare `macdoc` in an interactive terminal opens the dashboard** (`internal/cli/tui.go`, Bubble Tea). `macdoc doctor` always prints. Bare `macdoc` prints too when stdin or stdout isn't a TTY, with `--json`/`--quiet`, or on `TERM=dumb`.
- **Layout:**
  - Header: brand, machine, free space, scan age, active `@filter`, and a `read-only` marker.
  - Tab row: Overview, Suggestions, Toolchains, Space, Health.
  - Body.
  - A context-sensitive help footer, generated from the key bindings.
- **Suggestions:**
  - A selectable list grouped by tier.
  - At ≥ 120 columns, the selected item's `explain` view sits beside the list. Narrower, `enter` swaps the list for it.
- **`/` palette:** `/overview /wins /dev /space /health /scan /explain <id> /json /mcp /help /quit`, with prefix completion. `/json` shows the current view's data with JSON syntax highlighting. `/mcp` explains that Phase 8 isn't built yet.
- **`@` palette:** filters Suggestions and Toolchains by toolchain. It never accepts a path (safety invariant 4).
- **Loading:**
  - The real engine stages appear as a checklist with timings (`engine.OverviewOptions.Step`).
  - A scan shows a progress bar measured against the previous scan's size, plus bytes, files and the current folder.
  - `esc` cancels. Results of a stopped scan are discarded, and the previous results stay.
  - `q` while loading stops the engine before exiting.
- **Read-only:** the dashboard calls the same engine as the read-only commands and nothing else.
- **Theme** (`internal/cli/theme.go`):
  - Every color is a `CompleteAdaptiveColor`: truecolor, 256 and 16 values, for light and dark backgrounds. Lip Gloss picks the depth from the terminal.
  - Palette:
    - coral brand
    - cyan IDs and accents
    - SAFE green, CAUTION yellow, RISKY orange, ADVISE blue
    - muted and faint grays
  - `MACDOC_THEME=light|dark` skips background detection. `NO_COLOR` disables color.
  - Non-terminal output uses a plain theme that renders the exact pre-color text, with no boxes, dots or escapes, so pipes, files and golden tests stay stable.
  - Native commands are highlighted: tool green, flags yellow, arguments plain.

## 2. Report mockups

### `macdoc doctor`
```
macdoc · MacBook Air (M1, 8 GB) · macOS 15.x                     scan: 2h ago

Disk   ▕██████████████████████████▌▏ 236 / 245 GB used   9.1 GB free  (+3.2 GB purgeable)
RAM    8 GB · pressure HIGH · swap 6.2 GB (on disk)      Snapshots: 2 local

What matters most
  1. Free space is under 10 GB while swap is 6 GB. macOS may hang under load.
  2. 31.4 GB is safely reclaimable (caches and build artifacts).
  3. Docker Desktop VM is using 3.1 GB of RAM while idle.

Next:  macdoc wins     macdoc slow     macdoc plan --safe
Nothing has been changed. macdoc is read-only unless you run `apply`.
```

### `macdoc wins`
```
 Free & safe ──────────────────────────────────────────────── 31.4 GB
  O-ab12cd  14.2 GB  Xcode DerivedData · 9 projects idle 60d+       rebuilds on next build
  O-k3m9pq  11.0 GB  Docker build cache                              rebuilds layers
  O-x7v2rt   6.2 GB  npm · pnpm · Homebrew caches                    re-downloads on demand

 Worth considering ─────────────────────────────────────────── 27.8 GB
  O-p0q8ws  12.6 GB  node_modules in 23 projects idle 90d+           `npm i` to restore
  O-z4n1ty   8.4 GB  Android system images API 29, 30 (no AVD uses)   re-download ~1.2 GB each
  O-c6h5ju   6.8 GB  Ollama llama3:8b · unused 41 days               re-download 4.7 GB

 Your call (advice only) ───────────────────────────────────── 18.0 GB
            18.0 GB  iPhone backup (Mar 2025)        Finder › iPhone › Manage Backups

 macdoc explain <id> · macdoc plan --safe   (creates a dry-run; nothing is deleted)
```

### `macdoc plan --safe`
```
Plan P-7F3K · 3 items · 31.4 GB · expires in 30 min · origin: cli

 SAFE
  ✓ Xcode DerivedData (9 folders)          14.2 GB   → quarantine (restorable 7 days)
  ✓ Docker build cache                     11.0 GB   → docker builder prune -f
  ✓ npm · pnpm · Homebrew caches            6.2 GB   → npm cache clean --force
                                                       pnpm store prune
                                                       brew cleanup --prune=all

 Freed immediately: ~17.2 GB   Freed when quarantine expires/purged: ~14.2 GB
 ⓘ 2 local Time Machine snapshots may delay some space until macOS thins them.

 To apply:  macdoc apply P-7F3K
```

### `macdoc apply P-7F3K`
```
Re-checking 3 items… ✓ Xcode not running ✓ Docker reachable ✓ targets unchanged
Type "7F3K" to apply 31.4 GB (SAFE: 3 items): 7F3K

  ✓ Xcode DerivedData   → Q-91XZ           14.2 GB
  ✓ Docker build cache                     10.6 GB
  ✓ npm · pnpm · Homebrew caches            6.0 GB

Free space: 9.1 GB → 26.5 GB (+17.4 GB now, +14.2 GB held in quarantine until Nov 9)
Undo the quarantine part:  macdoc quarantine restore Q-91XZ      Log: macdoc log
```

## 3. Tone rules
- Use neutral words: "regenerable", "idle", "duplicate", "held". Never use "junk", "threat", "danger" or "clean your Mac now".
- Show **restore cost** on every line.
- Put a footer on every read-only screen saying "Nothing has been changed."
- Use color sparingly: green for SAFE, amber for CAUTION, plain for advice. Never use flashing or red alarm banners.
- Never show a fake "health score". Show real numbers with their source.
- Unknowns are explicit: `—  unmeasured (no permission)`.
- Keep `doctor` to 80 columns and 20 lines at most.

## 4. JSON contract
- Every command supports `--json` and outputs `{"schema_version": "1", "command": "...", "generated_at": "...", "scan": {...}, "data": {...}, "warnings": [...]}`.
- Byte values are integers. Every measurement has a `source`: `measured | tool | estimated | unknown`.
- JSON schemas live in `schema/` and are contract-tested. The MCP tools return the same shapes.
