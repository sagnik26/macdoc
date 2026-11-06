# macdoc: CLI UX

## 1. Commands

> **Implemented structure (2026-10-27):** one command, `macdoc doctor` (also bare `macdoc`), shows the full overview. Detail views are its sections: `doctor health | space [--large] | wins [--all] [--tier] | explain <id> | dev [toolchain] | projects [--idle N] | scan`. `macdoc doctor` is the short summary from the latest scan, and `doctor scan` rescans and prints the full report (every section). There is no `--scan` flag. The flat command list below is the original design. `plan`, `apply`, `quarantine` and `log` are top-level commands. `slow` and `trend` are doctor sections (`macdoc doctor slow`, `macdoc doctor trend`, Phase 7); in the dashboard they are the "Is memory slowing it down?" block on Health and "What grew" on Space (`/slow`, `/trend`).

| Command | Mode | Purpose |
|---|---|---|
| `macdoc` / `macdoc doctor` | read | One-screen health summary and top findings |
| `macdoc scan [--rescan] [--workers N] [--foreground]` | read | Run or refresh the full crawl. Other commands reuse a scan < 6 h old |
| `macdoc wins [--all] [--tier safe\|caution\|risky]` | read | Top space wins, tiered |
| `macdoc storage [--large]` | read | Category breakdown with "System Data" decoded. `--large` lists unknown big items |
| `macdoc dev [xcode\|android\|node\|docker\|python\|ai\|brew]` | read | Per-toolchain breakdown |
| `macdoc projects [--idle 90]` | read | Projects with idle days and artifact weight (built as `macdoc doctor projects`, §1b) |
| `macdoc doctor slow [--days 7]` | read (stores a vitals sample) | Verdict, biggest users against RAM, what each known group is, swap now and over the window |
| `macdoc explain <O-id>` | read | What it is, why it's there, restore cost, exact action |
| `macdoc doctor trend [--days 30]` | read | Growth per area (Xcode, Gradle, Docker, …) and the folders that grew most, between the latest scan and one about N days old. Sizes of folders a scan didn't store are bounds ("<105 MB", "or more") |
| `macdoc plan <O-id…> \| --safe \| --recipe <id>` | writes plan file | Dry-run plan, sealed |
| `macdoc apply <P-id>` | **mutating, TTY** | Confirm, re-validate, execute, verify |
| `macdoc quarantine list\|restore <Q-id>\|purge [--expired]` | restore/purge mutating, TTY | Manage quarantine |
| `macdoc log [--json]` | read | Audit history |
| `macdoc config [set\|protect\|unprotect\|path]` | writes macdoc's settings file | `plan_minutes`, `native_timeout_minutes`, and extra protected folders (add-only on top of the built-in list). No `edit`: opening an editor would run a program outside the executor; `config path` prints the file |
| `macdoc mcp` | read + Claude-origin plans | MCP stdio server for Claude Code / Claude Desktop (Phase 8). Prints a hint to stderr if started by hand |
| `macdoc schedule enable\|disable` | Phase 10 (built) | Writes or removes the daily read-only scan's launchd agent; the person runs the printed `launchctl` line in Terminal |

Global flags: `--json`, `--quiet`. (`--no-color` and `--verbose` were dropped as unused. Color follows the `NO_COLOR` convention instead.)

### 1a. The dashboard and colors (implemented 2026-10-28)

- **Bare `macdoc` in an interactive terminal opens the dashboard** (`internal/cli/dashboard.go`, Bubble Tea). `macdoc doctor` always prints. Bare `macdoc` prints too when stdin or stdout isn't a TTY, with `--json`/`--quiet`, or on `TERM=dumb`.
- **Welcome screen first** (`internal/cli/intro.go`). Nothing runs until the user chooses.
  - It shows:
    - A gradient ANSI-Shadow logo, or `◆ macdoc` when the window is under 34 rows.
    - The tagline.
    - The last scan, from `engine.LastScan`, which only reads macdoc's own store.
    - An action menu: `enter` check, `s` fresh scan, `?` help, `q` quit.
    - "How it works" in four steps, the promises, and the shell commands (at ≥ 40 rows).
  - Under 104 columns, the menu drops its descriptions and shows only the selected one's.
  - `/view` commands start the dashboard on that view. `/json` and `/explain` ask the user to check first.
- **Layout:**
  - Header: brand, machine, free space, scan age, and an `asks before removing` marker.
  - Tab row: Overview, Suggestions, Toolchains, Projects, Space, Health (`1`–`6`).
  - Body.
  - A context-sensitive help footer, generated from the key bindings.
- **Suggestions:**
  - Tier totals, then a selectable table (marker, ID, size, risk, suggestion, restore).
  - At ≥ 120 columns, the selected item's `explain` view sits beside the list. Narrower, `e` swaps the list for it.
- **`/` palette:** `/overview /scan /plan [safe|caution|risky] /wins /dev /projects /space /health /scan /explain <id> /json /mcp /help /quit`, with prefix completion. `/json` shows the current view's data with JSON syntax highlighting. `/mcp` shows how to connect Claude (`claude mcp add macdoc -- macdoc mcp`).
- **No `@` mentions.** A toolchain filter was tried and removed as unnecessary. The Toolchains view covers it. Nothing in the dashboard accepts a path (safety invariant 4).
- **Accessible tables** (`internal/cli/tables.go`). Every list in every view is a table:
  - Rounded border with column rules and an underlined, bold header row.
  - Right-aligned sizes and counts.
  - Text labels for risk and severity, so color is never the only signal.
  - A `▶` marker on the selected row, plus a background tint that keeps every foreground readable.
  - `fitColumns` shrinks only long text columns (wrap or cut with …), so IDs, sizes and risk labels are never truncated.
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

### 1b. `macdoc doctor projects` (implemented 2026-10-28, Phase 4)

```
macdoc doctor projects · scan S-TEST · 2h ago

 Projects ────────────────────────────────────────────────── 802 MB in 2
  ~/code/app · react-native, ios, android
    idle 120 days (git commit) · 800 MB · uncommitted changes
    node_modules 500 MB · ios/Pods 300 MB
  ~/Desktop/site · node
    idle time unknown · 2.0 MB · git clean
    .next 2.0 MB
    In a protected folder: listed only, macdoc never moves anything here.

 DerivedData of deleted projects ──────────────────────────────── 900 MB
     900 MB  ~/old/Gone.xcodeproj

 macdoc doctor wins · nothing has been changed
```
- One entry per project with at least 1 MB of build folders, biggest first. `--idle N` keeps projects known to be idle N+ days; an unknown idle time never counts as idle.
- **Idle** is the newest of: the last commit touching the project, the newest source file or folder (build folders, `.git` and caches excluded) and the newest build folder (a build or install is use). Access times are never used. The activity source is shown in brackets.
- **Git:** `git clean`, `uncommitted changes` (untracked Finder `.DS_Store` files don't count), `not in git`, or `git not checked` (projects with under 50 MB of build folders, or git failed).
- The dashboard's **Projects** tab shows the same as a table (Project, Idle, Size, Git, Biggest folders), with protected projects marked `(protected)`.
- `--json` returns `engine.ProjectsResult`: `projects[]` (root, name, kinds, artifacts, artifact_bytes, last_active, activity_source, idle_days, git, git_note, protected) and `derived_data[]` (path, bytes, workspace, project, missing).

### 1c. `macdoc plan` (implemented 2026-10-28, Phase 5)

- A top-level command, as designed: `macdoc plan <O-id…> | --safe | --tier safe|caution|risky | --recipe <id>`, plus `plan show <P-id>` and `plan list`. Exactly one selector. Arguments that look like paths are refused ("plans take suggestion IDs (O-…), never paths"), as are advice-only tiers and unknown recipes.
- Output: a header card (ID, item count, known size, minutes to expiry, origin and the request), then items grouped by tier. Each item shows ID, title, size, exactly one `→` line (`→ runs <argv>` or `→ moves to quarantine <path>`), what restoring costs, any raised-risk reason and the preconditions ("Checks first: Xcode isn't running · …"). Then totals split into "Freed right away" (tool commands) and "Moved to quarantine" (freed at purge), unknown sizes counted separately, a snapshot caveat when local snapshots exist, "Not included" with reasons, and the apply command (Phase 6) with the expiry time. Expired, applied and invalid plans say so and don't offer the apply command.
- **Dashboard:** on Suggestions, `space` marks (✓, with the cursor moving down; advice items refuse), the list header shows "✓ N marked · size · p plan", and `p` seals a plan from the marked items, or from the selected one. `/plan safe|caution|risky` plans a tier. The dry run opens as an overlay (`esc` closes) and the marks clear. It reuses the dashboard's opportunities when they're from the latest scan, so sealing takes a second or two.
- `--json`: `engine.PlanResult` (`plan`, `status`, `refused`, `snapshots`). `plan list --json`: store records.

### 1d. `macdoc apply`, `quarantine`, `log` (implemented 2026-10-28, Phase 6)

- `macdoc apply <P-id>` needs stdin and stdout to be a real terminal (an ioctl check, so pipes and `/dev/null` are refused) and has no `--json` and no `--yes`. It shows the dry run again (without the "To apply it" hint), then:
  - for a plan Claude proposed, a banner ("Claude proposed this plan. Confirm each item yourself.") and `[y/N]` for every item;
  - otherwise `[y/N]` only for RISKY items;
  - then `Type "7F3K" to apply N item(s), <size> (SAFE: n, CAUTION: n):`. The code is case-insensitive. Anything else prints "Not confirmed. Nothing has been changed."
- A Claude-origin plan starts with "Claude proposed this plan at 14:02 on 2 Nov. Confirm each item yourself." (local time; Phase 9), and its plan header says "proposed by Claude at 14:02 on 2 Nov".
- Progress is one line per item: `[i/n] O-id Title … done | moved to quarantine Q-XXXX | skipped: <reason> | failed: <reason>`. The summary has the counts, the measured free-space change, what native commands were expected to free, what went to quarantine (still on disk), why the measured number may be lower (snapshots, purgeable accounting, other writes), and a pointer to `macdoc log`. Ctrl-C stops before the next item.
- `macdoc quarantine [list]`: each entry with its state (held, expired, interrupted, not-moved, missing, damaged), size, original path, plan, recipe and expiry. Expired entries total "ready to purge" with the purge command, and folders without macdoc's record are listed as "Not macdoc's (never touched)". `--json`: `engine.QuarantineResult`.
- `macdoc quarantine restore <Q-id>` never overwrites. `macdoc quarantine purge --expired | <Q-id…>` lists what will go and asks for the word `purge` at a real terminal.
- `macdoc log [-n N]`: one line per audit record (plan, apply started, done / skipped / failed with reason, apply finished with the measured change, restored, purged). `--json` returns the raw records.
- **Dashboard remove (2026-10-28):** on Suggestions, `enter` on an item (or on the ones marked with `space`) seals a plan and opens a confirmation box: each item with its risk, size and path, the total, and "Enter remove · esc cancel". A second `enter` removes SAFE and CAUTION items; with any RISKY item the box asks to type the plan's 4-character code first. While it runs, the box shows each item as it finishes (removed, left alone with the reason, or failed). Afterwards the removed items leave the list and a flash gives "Removed n of m · freed X" (measured) and "Press r to rescan". `enter` on an advice-only item opens its explanation instead; `e` opens the explanation for any item. The footer is `enter remove • space mark • e explain • …`, and the header's status reads "asks before removing".
- **Delete at once:** `apply` and the dashboard delete each moved item right away (the quarantine is only a crash-safe staging step), so the plan's `→` line for a folder reads `→ deletes <path>` with "Not undoable by macdoc: …", the totals are one "Freed right away" line, and progress says `done`. An item whose purge is refused stays in quarantine and progress says `moved aside, not deleted: Q-XXXX`.
- **Dashboard (the long way):** after `p` seals a plan, the overlay offers `a apply`, and `/apply [P-id]` (default: the plan sealed in this session) does the same. The dashboard pauses (`tea.Exec`) and runs the same `runApply` in-process on the real terminal: same TTY check, dry run, `[y/N]` questions and typed code. It then waits for Enter and returns, with a flash pointing to `macdoc log` and `r` to rescan. Both this and the dashboard remove act only through `internal/executor`, and no subprocess is started.
- The `macdoc doctor` overview adds a Quarantine line when anything is held, with "ready to purge" for expired entries. It only reports.

## 2. Report mockups

These are design mockups; the README shows real output. The plan and apply mockups follow the delete-at-once behaviour (§1d).

### `macdoc doctor`
```
macdoc · MacBook Air (M1, 8 GB) · macOS 15.x                     scan: 2h ago

Disk   ▕██████████████████████████▌▏ 236 / 245 GB used   9.1 GB free  (+3.2 GB purgeable)
RAM    8 GB · pressure HIGH · swap 6.2 GB (on disk)      Snapshots: 2 local

What matters most
  1. Free space is under 10 GB while swap is 6 GB. macOS may hang under load.
  2. 31.4 GB is safely reclaimable (caches and build artifacts).
  3. Docker Desktop VM is using 3.1 GB of RAM while idle.

Next:  macdoc doctor wins     macdoc doctor slow     macdoc plan --safe
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

- Homebrew suggestions (`brew cleanup -n`, `brew autoremove -n`) may be up to 10 minutes old: a repeat `doctor` command reuses brew's last dry run (kept in macdoc's own database) while Homebrew's folders are unchanged. `apply` always re-checks with brew, and `macdoc plan` asks brew itself.

### `macdoc plan --safe`
```
Plan P-7F3K · 3 items · 31.4 GB · expires in 30 min · made by you (macdoc plan --safe)

 SAFE
  ✓ Xcode DerivedData (9 folders)          14.2 GB   → deletes ~/Library/Developer/Xcode/DerivedData/…
  ✓ Docker build cache                     11.0 GB   → docker builder prune -f
  ✓ npm · pnpm · Homebrew caches            6.2 GB   → npm cache clean --force
                                                       pnpm store prune
                                                       brew cleanup --prune=all

 Freed right away: ~31.4 GB
 ⓘ 2 local Time Machine snapshots may delay some space until macOS thins them.

 To apply:  macdoc apply P-7F3K
```

### `macdoc apply P-7F3K`
```
Re-checking 3 items… ✓ Xcode not running ✓ Docker reachable ✓ targets unchanged
Type "7F3K" to apply 31.4 GB (SAFE: 3 items): 7F3K

  ✓ Xcode DerivedData                      14.2 GB
  ✓ Docker build cache                     10.6 GB
  ✓ npm · pnpm · Homebrew caches            6.0 GB

Free space change (measured): +30.8 GB   Expected: 31.4 GB
Every step is in the audit log: macdoc log
```

## 3. Tone rules
- Use neutral words: "regenerable", "idle", "duplicate", "held". Never use "junk", "threat", "danger" or "clean your Mac now".
- Show **restore cost** on every line.
- Put a footer on every read-only screen saying "Nothing has been changed."
- Use color sparingly: green for SAFE, amber for CAUTION, plain for advice. Never use flashing or red alarm banners.
- Never show a fake "health score". Show real numbers with their source.
- Unknowns are explicit: `—  unmeasured (no permission)`.
- A measured suggestion size is explained once, in `doctor explain`: "This is the space removing it is certain to free. It can be less than the folder's total when its files share space with copies." A suggestion that would free under 1 MB is hidden with the note "N item(s) that would free under 1 MB not shown", a fully shared folder (a known 0) included; with the threshold off it is listed last, as 0 B. When a folder's files look shared with copies (allocated bytes exceed the size by more than 1% and 1 MB), `doctor explain` measures it on the spot and replaces that sentence with "Removing this frees at least 1.2 GB and at most 1.9 GB. The difference is space shared with copies of its files."; if the most could not be worked out it says "Removing this frees at least 1.2 GB. The most it could free could not be worked out." Lists, totals and plans keep the lower figure. The apply report says when the measured gain is above the expected size (the expected size is a floor), and names files that share space with copies among the reasons for a gain below it.
- Names from the disk are shown cleaned: terminal escapes and invisible characters are dropped and newlines become spaces (`display` in `theme.go`, built on `internal/textsafe`). Renderers start from that copy and never plan from it. `--json` shows the data as found.
- Keep `doctor` to 80 columns and 20 lines at most.

## 4. JSON contract
- Every command supports `--json` (except `apply`, which needs a person at a terminal) and outputs `{"schema_version": "1", "command": "...", "generated_at": "...", "data": {...}, "warnings": [...]}`. Scan details, where relevant, are inside `data`.
- Byte values are integers. Every measurement has a `source`: `measured | tool | estimated | unknown`.
- An opportunity's `bytes` (and so a plan item's `expected_bytes`) is the space removing it is certain to free: for a size measured from the scan, the node's private size when the volume reports it, else its allocated bytes (ADR 0005, stage 2). `explain --json` adds optional `bytes_lower` and `bytes_upper` when a range was measured (`bytes_upper` is absent when the most is unknown). `doctor projects` keeps `bytes` as allocated and adds `reclaimable_bytes` (omitted when unknown) on artifacts and DerivedData, and `reclaimable_bytes` on each project.
- JSON schemas live in `schema/` (one per command, generated from the Go types) and are contract-tested: `TestSchemasFrozen` fails on any change to an output type, and `TestJSONOutputMatchesSchema` validates real output. Additive changes regenerate them with `go test ./internal/cli -run TestSchemasFrozen -update`; renaming, removing or retyping a field bumps `schema_version`. The MCP tools return the same shapes.
