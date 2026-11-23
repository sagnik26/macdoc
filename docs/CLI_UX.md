# macdoc: CLI UX

## 1. Commands

> **Command set (2026-11-14, ADR 0012):** bare `macdoc` is the overview (the dashboard in a terminal, the summary when piped), plus five commands that answer one question each: `reclaim` (what can I get back), `retire` (what software and leftovers can I remove), `explore` (where did the space go, with `--growth` and `--projects`) and `vitals` (how is the Mac doing now, with `--memory` for the slowness verdict). `scan` rescans, and `apply` plans, confirms and applies in one command. The earlier `doctor` and `plan` commands were removed with no aliases. Older text below that says `doctor <section>` or `plan` means the command named in the table. `quarantine`, `log`, `config`, `schedule`, `mcp`, `completion` and `version` are unchanged. The "What grew" block is on Space, reached with `tab`.

| Command | Mode | Purpose |
|---|---|---|
| `macdoc` | read | One-screen health summary and top findings (dashboard in a terminal, summary when piped or with `--json`/`--quiet`) |
| `macdoc scan [--workers N] [--foreground]` | read | The full report. It always scans the disk afresh, with no question; other commands reuse a scan < 6 h old. In the dashboard, `/scan` does the same |
| `macdoc reclaim [toolchain] [--all] [--small] [--tier safe\|caution\|risky] [--toolchains] [--categories]` | read; cleans the chosen risk types at a real terminal (ADR 0013) | Top space wins, tiered. At a terminal (not `--json`, `--quiet`, piped or `--dry-run`) it lists everything by risk, then "What do you want to clean?" with a row each for SAFE, CAUTION and RISKY (count, size, note; RISKY only when something is in it). Type `safe`, `caution`, `risky` or `all` and press Enter to seal and apply through the executor; Enter alone is SAFE only, `n` or an unrecognised answer cancels. CAUTION only when named; RISKY only when named and it keeps apply's plan, y/N per item and typed code; advice-only items have no row (they are in the list above) and can never be chosen. `--tier X` skips the question and cleans only that tier on Enter. `--tier safe|caution --yes` cleans that tier with no prompt and no terminal (quarantine moves only; audit `non_interactive`); a bare `--yes` is refused. Administrator steps and Claude plans never. `--toolchains` and `--categories` stay read-only. `apply` is for specific IDs, other tiers and sealed plans. a toolchain name (xcode, android, node, homebrew, docker, python, ai, system) shows that toolchain and `--toolchains` all of them; `--categories` gives the category breakdown with "System Data" decoded and unknown big items |
| `macdoc explore --projects [--idle 90]` | read | Projects with idle days and artifact weight (§1b) |
| `macdoc explore [path]` | read | Folder explorer from the last scan (ExploreResult: entries, share, largest files, `complete`/`partial`/`unavailable`). Dashboard: `enter` on the Space tab |
| `macdoc retire` | read (stores a software snapshot with the scan) | Installed software, its data folders, the largest unowned folders under home (three levels deep, anywhere), and what is new since the previous scan (ADR 0007). A folder holding owned software is not listed whole; its unowned parts are listed instead |
| `macdoc vitals [--watch --interval D]` | read | Live status (StatusResult): health score and reasons, CPU, memory, disk and traffic, network, battery, top processes, process alerts (zombie parents, busy processes). `--watch` streams NDJSON (interval of at least 1s). the `/vitals` page shows the live block every 3s while open |
| `macdoc vitals --memory [--days 7] [--tips]` | read (stores a vitals sample) | Verdict, biggest users against RAM, what each known group is, swap now and over the window |
| `macdoc explore --growth [--days 30]` | read | Growth per area (Xcode, Gradle, Docker, …) and the folders that grew most, between the latest scan and one about N days old. Sizes of folders a scan didn't store are bounds ("<105 MB", "or more") |
| `macdoc quarantine list\|restore <Q-id>\|purge [--expired]` | restore/purge mutating, TTY | Manage quarantine |
| `macdoc log [--json]` | read | Audit history |
| `macdoc config [set\|protect\|unprotect\|hide\|unhide\|path]` | writes macdoc's settings file | `hide <id>` takes an `O-…` ID from the list or a recipe ID and adds it to `excluded_recipes` (it only subtracts); `unhide` removes it; the list's notes count hidden items; in the dashboard `x` on a suggestion hides its kind and reads the list again. `plan_minutes`, `native_timeout_minutes`, extra protected folders (add-only on top of the built-in list), and `excluded_recipes` (recipe IDs or `prefix.*` to turn off; only subtracts, shown in `config` and its JSON). No `edit`: opening an editor would run a program outside the executor; `config path` prints the file |
| `macdoc mcp` | read + Claude-origin plans | MCP stdio server for Claude Code / Claude Desktop (Phase 8). Prints a hint to stderr if started by hand |
| `macdoc schedule enable\|disable` | Phase 10 (built) | Writes or removes the daily read-only scan's launchd agent; the person runs the printed `launchctl` line in Terminal |

Global flags: `--json`, `--quiet`. (`--no-color` and `--verbose` were dropped as unused. Color follows the `NO_COLOR` convention instead.)

### 1a. The dashboard and colors (implemented 2026-10-28)

- **Bare `macdoc` in an interactive terminal opens the dashboard** (`internal/cli/dashboard.go`, Bubble Tea). Bare `macdoc` prints when stdin or stdout isn't a TTY, with `--json`/`--quiet`, or on `TERM=dumb`.
- **Welcome screen first** (`internal/cli/intro.go`). Nothing runs until the user chooses.
  - It shows:
    - A gradient ANSI-Shadow logo, or `◆ macdoc` when the window is under 34 rows.
    - The tagline.
    - The last scan, from `engine.LastScan`, which only reads macdoc's own store.
    - A box listing every command (`scan`, `reclaim`, `retire`, `explore`, `vitals`) with a one-line explanation; `↑↓` choose and `enter` runs the highlighted one.
    - The promises. ("How it works" and "From the shell" were removed on 2026-11-16.)
  - Under 104 columns, the menu drops its descriptions and shows only the selected one's.
  - `/slow` starts the dashboard on that view.
- **Layout:**
  - Header: brand, machine, free space, scan age, and an `asks before removing` marker.
  - Tab row (the scan view only): Overview, Suggestions, Toolchains, Projects, Space (`1`–`5`). Every other command opens a page of its own with the brand, its title and no tabs, and works without the scan view.
  - Body.
  - A context-sensitive help footer, generated from the key bindings.
- **Suggestions:**
  - Tier totals, then a selectable table (marker, ID, size, risk, suggestion, restore).
  - At ≥ 120 columns, the selected item's detail view sits beside the list. Narrower, `e` swaps the list for it.
- **`/` palette:** `/scan /reclaim /retire /explore /vitals`, with prefix completion. `/reclaim` asks nothing: it scans afresh, then, as soon as the scan ends, starts removing every SAFE and CAUTION suggestion (never RISKY, advice or an installed app) and shows progress by category (SAFE, then CAUTION), with no second bar after the scan's; `esc` stops it after the current item, and what it moved can be restored. When it ends, RISKY items it left alone are counted on the result screen ("N RISKY items left alone"); `d` there opens the normal removal box for them, where each one's ID must be typed (the table of suggestions is on the scan view, not repeated here); cleaning starts with Enter on a suggestion on the Suggestions tab, or with `/reclaim`. `/help` and `/quit` are not menu entries: the footer shows `? help` and `q quit` (and `/help`, `/quit` while typing), and typing them still works. `/vitals` is the live page (score, CPU, memory, disk, network, battery, biggest memory users). A "● Live · updated Ns ago" line shows it is current (the clock ticks every second, a reading is taken every 3), each number has an arrow for the way it moved since the last reading; a red alert appears only when memory pressure is warn or critical, or the disk is low (under 10 GB free) with high swap (2 GiB or more). With the alert, a "How macdoc decides if memory is slowing your Mac" box shows where pressure sits (Normal / Warn / Critical, marked "▲ now") and swap and free disk against their limits; `w` shows or hides the same box when there is no alert, `/explore` opens the folder tree (read-only, from the last scan; `→`/`enter` opens a folder in place, `←` closes it), and `/refresh` lists the maintenance steps as a table (ID, step, whether it needs your Mac password, risk, what happens afterwards, an Apply button) with the highlighted step explained below; Enter seals a plan for that step and hands it to apply, which asks you to confirm in Terminal. `/retire` shows the `retire` list in an overlay, read in the background: a summary (cleanups that can be reclaimed, new programs, the largest folder no software explains), tables filtered by `f`, `c` to mark the cleanups in Suggestions (nothing is removed until you plan and confirm), and `enter` on a program for its folders and cleanups. View-only tabs have no command; reach them with `tab` or `1`–`5`.
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
- **Theme** (`internal/cli/theme.go`, palette in `docs/DESIGN_SYSTEM.md`):
  - Every color is a truecolor value for a dark and for a light background, reduced to 256 or 16 colors for terminals that show fewer (Charm v2: `colorprofile`).
  - Palette:
    - indigo brand (`#7571F9` dark, `#5A56E0` light) for the mark, headings, the active tab and IDs
    - a brand-to-pink gradient on the header rule, the welcome logo and the progress bar
    - ANSI 212 (dark) or 162 (light) for the selected row's marker, ANSI 62 on 230 for the page title chip
    - SAFE green, CAUTION yellow, RISKY orange, ADVISE and info blue, errors red (these never change with the look)
    - muted and faint grays
  - `MACDOC_THEME=light|dark` skips background detection. `NO_COLOR` disables color. A colored dashboard also asks the terminal for its background color at start and restyles if the answer differs from the guess.
  - Non-terminal output uses a plain theme with no color and no attributes, so it carries no escape sequence, with no boxes or dots, so pipes, files and golden tests stay stable. The golden snapshots are in `internal/cli/tests/testdata/golden`.
  - Native commands are highlighted: tool green, flags yellow, arguments plain.

### 1b. `macdoc explore --projects` (implemented 2026-10-28, Phase 4)

```
macdoc explore --projects · scan S-TEST · 2h ago

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

 macdoc reclaim · nothing has been changed
```
- One entry per project with at least 1 MB of build folders, biggest first. `--idle N` keeps projects known to be idle N+ days; an unknown idle time never counts as idle.
- **Idle** is the newest of: the last commit touching the project, the newest source file or folder (build folders, `.git` and caches excluded) and the newest build folder (a build or install is use). Access times are never used. The activity source is shown in brackets.
- **Git:** `git clean`, `uncommitted changes` (untracked Finder `.DS_Store` files don't count), `not in git`, or `git not checked` (projects with under 50 MB of build folders, or git failed).
- The dashboard's **Projects** tab shows the same as a table (Project, Idle, Size, Git, Biggest folders), with protected projects marked `(protected)`.
- `--json` returns `engine.ProjectsResult`: `projects[]` (root, name, kinds, artifacts, artifact_bytes, last_active, activity_source, idle_days, git, git_note, protected) and `derived_data[]` (path, bytes, workspace, project, missing).

> **Removed 2026-11-24 (ADR 0015):** `macdoc apply`, `/plan`, `/apply`, the `p` and `a` keys, the plan overlay and `macdoc refresh` with its `/refresh` page no longer exist. Cleaning is `macdoc reclaim` or Enter on a suggestion in the dashboard; a plan is still sealed inside every removal, but nobody creates or applies one by name. The sections below describe the earlier design and are kept for history.

### 1c. `macdoc apply --dry-run` (was `macdoc plan`) (implemented 2026-10-28, Phase 5)

- A top-level command, as designed: `macdoc apply <O-id…> | --safe | --tier safe|caution|risky | --recipe <id>` (with `--dry-run` it only seals and shows the plan), plus `apply --show <P-id>` and `apply --list`. Exactly one selector. Arguments that look like paths are refused ("plans take suggestion IDs (O-…), never paths"), as are advice-only tiers and unknown recipes.
- Output: a header card (ID, item count, known size, minutes to expiry, origin and the request), then items grouped by tier. Each item shows ID, title, size, exactly one `→` line (`→ runs <argv>` or `→ moves to quarantine <path>`), what restoring costs, any raised-risk reason and the preconditions ("Checks first: Xcode isn't running · …"). Then totals split into "Freed right away" (tool commands) and "Moved to quarantine" (freed at purge), unknown sizes counted separately, a snapshot caveat when local snapshots exist, "Not included" with reasons, and the apply command (Phase 6) with the expiry time. Expired, applied and invalid plans say so and don't offer the apply command.
- **Dashboard:** on Suggestions, `space` marks (✓, with the cursor moving down; advice items refuse), the list header shows "✓ N marked · size · p plan", and `p` seals a plan from the marked items, or from the selected one. `/plan safe|caution|risky` plans a tier. The dry run opens as an overlay (`esc` closes) and the marks clear. It reuses the dashboard's opportunities when they're from the latest scan, so sealing takes a second or two.
- `--json`: `engine.PlanResult` (`plan`, `status`, `refused`, `snapshots`). `apply --list --json`: store records.

### 1d. `macdoc apply` (existing plan), `quarantine`, `log` (implemented 2026-10-28, Phase 6)

- `macdoc apply <P-id>` needs stdin and stdout to be a real terminal (an ioctl check, so pipes and `/dev/null` are refused) and has no `--json` and no `--yes`. It shows the dry run again (without the "To apply it" hint), then:
  - for a plan Claude proposed, a banner ("Claude proposed this plan. Confirm each item yourself.") and `[y/N]` for every item;
  - otherwise `[y/N]` only for RISKY items;
  - then `Type "7F3K" to apply N item(s), <size> (SAFE: n, CAUTION: n):`. The code is case-insensitive. Anything else prints "Not confirmed. Nothing has been changed."
- A Claude-origin plan starts with "Claude proposed this plan at 14:02 on 2 Nov. Confirm each item yourself." (local time; Phase 9), and its plan header says "proposed by Claude at 14:02 on 2 Nov".
- Progress is one line per item: `[i/n] O-id Title … done | moved to quarantine Q-XXXX | skipped: <reason> | failed: <reason>`. The summary has the counts, the measured free-space change, what native commands were expected to free, what went to quarantine (still on disk), why the measured number may be lower (snapshots, purgeable accounting, other writes), and a pointer to `macdoc log`. Ctrl-C stops before the next item.
- `macdoc quarantine [list]`: each entry with its state (held, expired, interrupted, not-moved, missing, damaged), size, original path, plan, recipe and expiry. Expired entries total "ready to purge" with the purge command, and folders without macdoc's record are listed as "Not macdoc's (never touched)". `--json`: `engine.QuarantineResult`.
- `macdoc quarantine restore <Q-id>` never overwrites. `macdoc quarantine purge --expired | <Q-id…>` lists what will go and asks for the word `purge` at a real terminal.
- `macdoc log [-n N]`: one line per audit record (plan, apply started, done / skipped / failed with reason, apply finished with the measured change, restored, purged). `--json` returns the raw records. Each record has a `run` ID (`R-xxxxxxxx`) shared by everything one command wrote (the plan, or the apply's start, items and finish, or a restore or purge), plus the `plan` and `item` IDs, so a run can be matched to its plan and items.
- **Timeouts:** read-only external commands stop after 10 s by default. `MACDOC_TIMEOUT=<seconds>` changes that for all of them, and `MACDOC_TIMEOUT_<COMMAND>=<seconds>` for one (command name upper-cased, other characters `_`, such as `MACDOC_TIMEOUT_SYSTEM_PROFILER`). Only whole seconds from 1 to 600 are accepted; anything else is ignored. Cleanup commands keep the `config` setting. Touch ID for sudo stays advice-only (ADR 0009).
- **Dashboard remove (2026-10-28):** on Suggestions, `enter` on an item (or on the ones marked with `space`) seals a plan and opens a confirmation box: each item with its risk, size and path, the total, and "Enter remove · esc cancel". A second `enter` removes SAFE and CAUTION items; with any RISKY item the box asks to type the plan's 4-character code first. While it runs, the box shows each item as it finishes (removed, left alone with the reason, or failed). Afterwards the removed items leave the list and a flash gives "Removed n of m · freed X" (measured) and "Press r to rescan". `enter` on an advice-only item opens its explanation instead; `e` opens the explanation for any item. The footer is `enter remove • space mark • e explain • …`, and the header's status reads "asks before removing".
- **Dashboard range (ADR 0005, stage 3):** when the detail pane shows a measured folder whose files may share space with copies, the dashboard measures the range in the background. The pane shows a one-line "measuring the range…" under the size note, then replaces the note with "Removing this frees at least X and at most Y. The difference is space shared with copies of its files." If the walk fails, is skipped or finds nothing shared, the usual note stays and no error is shown. The keys never wait for it: moving the selection, switching tab, closing the detail or rescanning cancels the walk, and results are kept per suggestion until the next scan. The walked path is the suggestion's own, never typed.
- **Delete at once:** `apply` and the dashboard delete each moved item right away (the quarantine is only a crash-safe staging step), so the plan's `→` line for a folder reads `→ deletes <path>` with "Not undoable by macdoc: …", the totals are one "Freed right away" line, and progress says `done`. An item whose purge is refused stays in quarantine and progress says `moved aside, not deleted: Q-XXXX`.
- **Dashboard (the long way):** after `p` seals a plan, the overlay offers `a apply`, and typing `/apply [P-id]` (default: the plan sealed in this session) does the same. The dashboard pauses (`tea.Exec`) and runs the same `runApply` in-process on the real terminal: same TTY check, dry run, `[y/N]` questions and typed code. It then waits for Enter and returns, with a flash pointing to `macdoc log` and `r` to rescan. Both this and the dashboard remove act only through `internal/executor`, and no subprocess is started.
- The `macdoc` overview adds a Quarantine line when anything is held, with "ready to purge" for expired entries. It only reports.

## 2. Report mockups

These are design mockups; the README shows real output. The plan and apply mockups follow the delete-at-once behaviour (§1d).

### `macdoc` (overview)
```
macdoc · MacBook Air (M1, 8 GB) · macOS 15.x                     scan: 2h ago

Disk   ▕██████████████████████████▌▏ 236 / 245 GB used   9.1 GB free  (+3.2 GB purgeable)
RAM    8 GB · pressure HIGH · swap 6.2 GB (on disk)      Snapshots: 2 local

What matters most
  1. Free space is under 10 GB while swap is 6 GB. macOS may hang under load.
  2. 31.4 GB is safely reclaimable (caches and build artifacts).
  3. Docker Desktop VM is using 3.1 GB of RAM while idle.

Next:  macdoc reclaim     macdoc vitals --memory
Nothing has been changed. macdoc is read-only unless you clean something with `reclaim`.
```

### `macdoc reclaim`
```
 Free & safe ──────────────────────────────────────────────── 31.4 GB
  O-ab12cd  14.2 GB  Xcode DerivedData · 9 projects idle 60d+       rebuilds on next build
  O-k3m9pq  11.0 GB  Docker build cache                              rebuilds layers
  O-x7v2rt   6.2 GB  npm · pnpm · Homebrew caches                    re-downloads on demand

 Worth considering ─────────────────────────────────────────── 27.8 GB
  O-p0q8ws  12.6 GB  node_modules in 23 projects idle 90d+           `npm i` to restore
  O-z4n1ty   8.4 GB  Android system images API 29, 30 (no AVD uses)   re-download ~1.2 GB each
  O-c6h5ju   6.8 GB  Ollama llama3:8b · unused 41 days               re-download 4.7 GB

 Do it yourself (macdoc can't clean these) ───────────────────────────────────── 18.0 GB
            18.0 GB  iPhone backup (Mar 2025)        Finder › iPhone › Manage Backups

 macdoc reclaim   (asks before anything is removed)
```

- Homebrew suggestions (`brew cleanup -n`, `brew autoremove -n`) may be up to 10 minutes old: a repeat command reuses brew's last dry run (kept in macdoc's own database) while Homebrew's folders are unchanged. `apply` always re-checks with brew, and `macdoc apply` asks brew itself.

> **Removed 2026-11-24 (ADR 0015):** `macdoc apply`, `/plan`, `/apply`, the `p` and `a` keys, the plan overlay and `macdoc refresh` with its `/refresh` page no longer exist. Cleaning is `macdoc reclaim` or Enter on a suggestion in the dashboard; a plan is still sealed inside every removal, but nobody creates or applies one by name. The sections below describe the earlier design and are kept for history.

## 3. Tone rules
- Use neutral words: "regenerable", "idle", "duplicate", "held". Never use "junk", "threat", "danger" or "clean your Mac now".
- Show **restore cost** on every line.
- Put a footer on every read-only screen saying "Nothing has been changed."
- Use color sparingly: green for SAFE, amber for CAUTION, plain for advice. Never use flashing or red alarm banners.
- Never show a fake "health score". Show real numbers with their source.
- Unknowns are explicit: `—  unmeasured (no permission)`.
- A measured suggestion size is explained once, in the Suggestions detail pane (`e`): "This is the space removing it is certain to free. It can be less than the folder's total when its files share space with copies." A suggestion that would free under 1 MB is hidden with the note "N item(s) that would free under 1 MB not shown", a fully shared folder (a known 0) included; with the threshold off it is listed last, as 0 B. When a folder's files look shared with copies (allocated bytes exceed the size by more than 1% and 1 MB), the dashboard measures it in the background and replaces that sentence with "Removing this frees at least 1.2 GB and at most 1.9 GB. The difference is space shared with copies of its files."; if the most could not be worked out it says "Removing this frees at least 1.2 GB. The most it could free could not be worked out." Lists, totals and plans keep the lower figure. The apply report says when the measured gain is above the expected size (the expected size is a floor), and names files that share space with copies among the reasons for a gain below it.
- Names from the disk are shown cleaned: terminal escapes and invisible characters are dropped and newlines become spaces (`display` in `theme.go`, built on `internal/textsafe`). Renderers start from that copy and never plan from it. `--json` shows the data as found.
- Keep the overview to 80 columns and 20 lines at most.

## 4. JSON contract
- Every command supports `--json` (cleaning itself needs a person at a terminal) and outputs `{"schema_version": "3", "command": "...", "generated_at": "...", "data": {...}, "warnings": [...]}`. Scan details, where relevant, are inside `data`.
- Byte values are integers. Every measurement has a `source`: `measured | tool | estimated | unknown`.
- An opportunity's `bytes` (and so a plan item's `expected_bytes`) is the space removing it is certain to free: for a size measured from the scan, the node's private size when the volume reports it, else its allocated bytes (ADR 0005, stage 2). the suggestion's detail (`e`) carries optional `bytes_lower` and `bytes_upper` when a range was measured (`bytes_upper` is absent when the most is unknown). `explore --projects` keeps `bytes` as allocated and adds `reclaimable_bytes` (omitted when unknown) on artifacts and DerivedData, and `reclaimable_bytes` on each project.
- JSON schemas live in `schema/` (one per command, generated from the Go types) and are contract-tested: `TestSchemasFrozen` fails on any change to an output type, and `TestJSONOutputMatchesSchema` validates real output. Additive changes regenerate them with `go test ./internal/cli -run TestSchemasFrozen -update`; renaming, removing or retyping a field bumps `schema_version`. The MCP tools return the same shapes.

## Discovery sources (`retire`, `/retire`)

Each source reads one place software is installed. Every query is a fixed, read-only command, or a read of a folder. A source that can't run is `absent` (its tool isn't installed); one that fails or times out is `unknown`, with the first line of the error as the reason.

| Category | Source | What it reads |
|---|---|---|
| Apps and system packages | `apps` | `.app` bundles in `/Applications` and `~/Applications` (`Info.plist`) |
| | `brew` | `brew list --formula` and `--cask` |
| | `pkgutil` | installer receipts: `pkgutil --pkgs`, then `--pkg-info` for each |
| | `mas` | App Store apps: `mas list` |
| Language packages | `npm` | `npm ls -g --depth=0 --json` |
| | `pip` | `pip3 list --format=json` |
| | `pipx` | `pipx list --json` |
| | `uv-tool` | `uv tool list` |
| | `gem` | `gem list --local --no-details` |
| | `cargo` | `cargo install --list` |
| Runtimes | `runtimes` | version folders: nvm (`~/.nvm/versions/node`), pyenv, rustup toolchains, and Java JDKs (`/Library/Java/JavaVirtualMachines` and the per-user folder) |
| Programs on PATH | `tools` | executables in the folders on `PATH` that you own; a copy that a package manager already lists is counted once, under the package manager |
| Images and models | `docker` | `docker images` (needs the daemon running) |
| | `ollama` | `ollama list` |

Each install is counted once: a PATH program already listed by a package manager is left out, and an App Store entry for an app that's installed as a bundle is left out.

**Not covered yet:** conda environments and packages, Poetry's own installs, Composer, Maven and Gradle's global tools, Homebrew taps, Docker containers (only images are listed), and anything installed by hand into an unusual folder. These need their own source and tests before they appear.
