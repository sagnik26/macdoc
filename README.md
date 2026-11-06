# macdoc

> A safe, local-first macOS CLI that explains why a developer Mac is full and slow, and gets space back without surprises.

**Status:** in development. **Phases 0–6 are complete; Phases 7–10 are built (Phase 7's dogfood week ended 2026-11-04 and it isn't signed off yet: 0.88 of the 10 GB reclaim target); Phase 11, open-source readiness, is in progress** (see [plan.md](plan.md)). It isn't released: there are no tags or binaries yet, so you build it from source. Diagnosis works end to end, you can make sealed dry-run cleanup plans, and you can remove items: press Enter on a suggestion in the dashboard and Enter again to confirm (each RISKY item needs its ID typed), or use `macdoc apply` in Terminal. Removed items are deleted, so the space is freed at once, and every change is in an audit log. · **Started:** 2026-10-26 · **Language:** Go 1.27 · `macdoc` is a working name.

---

## What works today

macdoc has **two everyday commands**:

| Command | When to use it | What you get | Time |
|---|---|---|---|
| **`macdoc`** | Any time: "how is my Mac doing?" | **A welcome screen, then the interactive dashboard** (in a terminal): overview, a browsable table of suggestions with details, toolchains, projects, space (with what grew since an earlier scan) and health (with whether memory is slowing your Mac down). Nothing runs until you choose. Scans automatically only if there's no scan yet | instant, then ~7–15 s to load |
| **`macdoc doctor scan`** | After installing or cleaning things, or when the summary says the scan is old | **Fresh scan + the full report:** everything in the summary, plus scan details, every health finding and the top apps, space by category with "System Data" decoded, big items no recipe covers, every suggestion with its ID, and per-toolchain totals | ~45 s–2 min (background priority) |

`macdoc doctor` prints the **short summary** instead of opening the dashboard. Bare `macdoc` does the same when its output is piped, with `--json` or `--quiet`, or on `TERM=dumb`.

### `macdoc`: the welcome screen
`macdoc` opens on an introduction and **does nothing until you choose**. It shows:
- The macdoc logo and a one-line description.
- The last scan's age and size. On the first run it says a scan will happen.
- A menu:
  - **`enter`** check my Mac (loads the latest scan)
  - **`s`** fresh scan first
  - **`?`** keys and commands
  - **`q`** quit
- "How it works" in four steps: Check, Explore, Understand, Clean up.
- macdoc's promises.
- The shell commands, when the window is tall enough.

`/` commands work here too: for example, `/space` opens the dashboard on the Space view. Small windows get a compact layout.

### `macdoc`: the dashboard
```
 ◆ macdoc  MacBookAir10,1 · M1                             51.2 GB free · scan 6m ago · asks before removing
  1 Overview   2 Suggestions   3 Toolchains   4 Projects   5 Space   6 Health
──────────────────────────────────────────────────────────────────────────────────────────────────────────────
 SAFE 825 MB  ·  CAUTION 553 MB  ·  ADVICE 48.9 GB   item 1 of 12
 ╭───┬──────────┬─────────┬─────────┬────────────────────────────────┬──────────────────────────────╮
 │   │ ID       │    Size │ Risk    │ Suggestion                     │ Restore                      │
 ├───┼──────────┼─────────┼─────────┼────────────────────────────────┼──────────────────────────────┤
 │ ▶ │ O-gmyov5 │  394 MB │ SAFE    │ uv cache                       │ re-downloads on demand       │
 │   │ O-kcxrej │  412 MB │ SAFE    │ Homebrew old versions and dow… │ re-downloads on demand       │
 │   │ O-gg6lrb │  197 MB │ CAUTION │ Android build-tools 34.0.0     │ re-downloads on demand       │
 │   │ O-dkuv2w │ 33.7 GB │ ADVICE  │ Leftover iOS simulator runtim… │ Runtimes can be downloaded … │
 ╰───┴──────────┴─────────┴─────────┴────────────────────────────────┴──────────────────────────────╯
 enter remove • space mark • e explain • ↑↓ move • tab switch view • / commands • r rescan • ? help • q quit
```
- **Every list is a table:**
  - Each table has a header row and a rounded border.
  - Sizes and counts are right-aligned.
  - Risk (`SAFE`, `CAUTION`, `RISKY`, `ADVICE`) and severity (`Critical`, `Warning`, `Info`) are spelled out, never shown by color alone.
  - The selected row has a `▶` marker as well as a highlight.
  - On narrow terminals only the long text columns shrink (cut with … or wrapped), so IDs, sizes and risk labels always show in full.
- **What each view shows:**
  - **Overview:** your Mac (disk, memory, pressure, swap, snapshots), the top findings, reclaimable space per tier, where your space is.
  - **Suggestions:** every item.
  - **Toolchains:** a summary plus one table per toolchain.
  - **Projects:** every project with build folders: how long it's been idle (last commit, file change or build), their size, git state (clean or uncommitted changes) and the biggest folders. Projects in protected folders such as ~/Desktop are listed but never suggested.
  - **Space:** by category with per-risk columns, **what grew** since a scan about 30 days old (areas and folders), "System Data" decoded, large items with no recipe.
  - **Health:** **"Is memory slowing it down?"** (a verdict, the biggest users against your RAM, swap now and over the last days), findings with explanations, and the top memory users with what each one is.
- **Views:** `tab`/`shift+tab` or `1`–`6`. On wide terminals (≥ 120 columns) the selected suggestion's details sit beside the table. On narrower ones, `e` opens them and `esc` goes back.
- **`/` commands**, with completion (`tab` completes, `↑↓` picks): `/overview` `/scan` `/plan` `/wins` `/dev` `/projects` `/space` `/health` `/slow` `/trend` `/explain <id>` `/json` (the current view as syntax-highlighted JSON) `/mcp` `/help` `/quit`.
- **Scanning:** `r` or `/scan` rescans with a live progress bar (bytes, files, current folder, % of the last scan's size). `esc` stops a scan, and the previous results stay on screen.
- **Footer:** shows the keys for the current context. `?` lists every key and command.
- **Plans:** on Suggestions, `space` marks items (✓) and `p` seals a dry-run plan from them (or from the selected item). `/plan safe` plans a whole tier. The plan opens as a dry run with its ID. Making a plan changes nothing.
- **Removing:** on Suggestions, press `enter` on an item (or mark several with `space` first). A box shows what will go and how much space comes back. Press `enter` again to remove it, or `esc` to cancel. RISKY items also ask you to type a short code. Each item is checked again just before it's removed and skipped if it changed. Removed items are deleted for good, so the space is freed at once; the result shows the measured free-space change. Advice-only items can't be removed (macdoc takes no action on them), so `enter` explains them.
- **Plans the long way:** `p` seals a dry-run plan instead, and `a` (or `/apply`) runs `macdoc apply` on it in the terminal with the typed code. `q` quits.

**Colors:** a coral/cyan palette with green, yellow, orange and blue for the SAFE, CAUTION, RISKY and ADVISE tiers. It uses truecolor where available and falls back to 256 and then 16 colors automatically, with separate shades for light and dark terminals. `NO_COLOR=1` turns color off. `MACDOC_THEME=light|dark` overrides the background detection. Piped output and files are always plain text.

### `macdoc doctor`: the short summary
```
$ macdoc doctor
╭─────────────────────────────────────────────────────────────────────╮
│ ◆ macdoc · MacBookAir10,1 (M1, 8.0 GB) · macOS 26.6.2 · scan 9m ago │
╰─────────────────────────────────────────────────────────────────────╯

Disk       ━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 194 GB / 245 GB used   51.3 GB free  (+3.4 GB purgeable)
Memory     8.0 GB · pressure ● warn · 39% free · swap 3.6 GB used
Snapshots  0 local Time Machine snapshot(s)

What matters most
  1. ▲ 3.6 GB of swap in use.
  2. ▲ Memory pressure is elevated.
  3. • Google Chrome is using 1.6 GB of RAM.

Where your space is (Data volume, 128 GB measured)
  /Users 52.3 GB · /System 39.0 GB · /Library 19.0 GB · /opt 7.5 GB · /private 5.2 GB · 36.7 GB not visible (needs Full Disk Access)

Reclaimable                              1.4 GB actionable · 48.9 GB advice
  ● Free & safe           825 MB   Homebrew old versions and downloads 412 MB · uv cache 394 MB · 4 × Old app logs 19.6 MB
  ● Worth considering     553 MB   2 × Old Android build-tools 392 MB · Node.js v20.20.2 (nvm) 161 MB · pip cache 8.2 KB
  ● Your call            48.9 GB   2 × Leftover simulator runtime data 48.9 GB

More: macdoc doctor scan (rescan + full report) · wins (every item + ID) · explain <id> · space · health
✓ Nothing has been changed.
```
In a terminal it's colored: the disk bar turns yellow at 80% and red at 90%, tiers are color-coded, IDs are cyan, and commands are syntax-highlighted. The boxes, dots and colors only appear in a terminal.

### `macdoc doctor scan`: the full report
It rescans the Data volume at background priority, so your Mac stays responsive, with a live progress line. Ctrl-C keeps a partial scan. Then it prints every section in order:

```
OVERVIEW         the short summary above
SCAN             files and bytes measured, how much of the volume was visible, where the space actually is, what couldn't be read
HEALTH           every finding with its explanation, top memory users grouped by app/dev tool
SPACE            reclaimable space by category, "System Data" decoded, big items no recipe covers
ALL SUGGESTIONS  every item with its ID, size, restore cost and exact command
BY TOOLCHAIN     totals for xcode, android, node, homebrew, docker, python, ai, system
```
Options: `--foreground` (faster, competes with your apps) and `--workers N`.

### Single sections, when you only want one part
These read the latest scan and don't rescan.

| Command | Shows |
|---|---|
| `macdoc doctor health` | Disk, memory, swap, snapshots, every finding, top memory users |
| `macdoc doctor slow [--days 7]` | Is memory what's slowing your Mac down? A verdict, the biggest users against your RAM with what each one is, and swap now and over the last days (each check stores a small sample). It never quits an app |
| `macdoc doctor trend [--days 30]` | What grew between the latest scan and one about 30 days old: per area (Xcode, Gradle, Docker, …) and the folders that grew most |
| `macdoc doctor space [--large]` | Reclaimable space by category and "System Data" decoded. `--large` adds big items no recipe covers |
| `macdoc doctor wins [--all] [--tier safe\|caution\|risky\|advise]` | Every suggestion with its ID. `--all` lists repeated items individually |
| `macdoc doctor explain <id>` | One suggestion in detail: what it is, why it's there, risk, idle time, how to restore, the exact action |
| `macdoc doctor dev [toolchain]` | Suggestions for one toolchain (xcode, android, node, homebrew, docker, python, ai, system) |
| `macdoc doctor projects [--idle N]` | Your projects: idle days, build folders (node_modules, Pods, iOS/Android builds, .venv, .next, DerivedData), git state. `--idle 90` shows only projects idle 90+ days |

Every command supports `--json`, which outputs one versioned JSON document with top-level warnings. Its shape is frozen per command in [`schema/`](schema/) (JSON Schema, `schema_version` 1). `--quiet` hides the progress line. Other top-level commands: `macdoc version`, `macdoc config` (settings and your protected folders), `macdoc completion zsh|bash|fish`.

### `macdoc plan`: a sealed dry run
```
$ macdoc plan --safe
Plan P-ZDHP · 4 item(s) · 825 MB · expires in 30 min · made by you (macdoc plan --safe)

 SAFE ─────────────────────────────────────────────────────────── 825 MB
  O-5wx6bf  Homebrew old versions and downloads             412 MB
            → runs brew cleanup --prune=all
            Not undoable by macdoc: re-downloads on demand. Homebrew downloads a version again if you reinstall it.
  O-uz4vkv  Old app logs · Claude                          10.7 MB
            → deletes ~/Library/Logs/Claude
            Not undoable by macdoc: nothing to restore. Old logs aren't needed for apps to work.
  …
 Freed right away: 825 MB

 Nothing has been changed.
 To apply it: macdoc apply P-ZDHP · expires 15:41
```
| Command | Does |
|---|---|
| `macdoc plan O-ab23cd O-k3m9pq` | Plans these suggestions (IDs from `macdoc doctor wins`; paths are refused) |
| `macdoc plan --safe` · `--tier caution` · `--recipe <id>` | Plans a whole tier or recipe (advice-only items are never planned) |
| `macdoc plan show <P-id>` | Shows a plan again with its status: pending, expired, applied or invalid |
| `macdoc plan list` | Recent plans |

A plan re-checks every target as it is now. It must still exist, not be a symlink, and pass the allowlist and protected-path rules; anything else is left out with a reason. It records each folder's fingerprint (device, inode, modification time, size) and the exact command or move, then seals it with SHA-256. The hash is stored in the plan file and in macdoc's database. Plans expire after 30 minutes. Editing a plan file by hand makes it invalid.

### `macdoc apply`: carry out a plan, after you confirm
```
$ macdoc apply P-ZDHP
… the dry run again …
 Type "ZDHP" to apply 4 item(s), 825 MB (SAFE: 4): ZDHP

 [1/4] O-5wx6bf Homebrew old versions and downloads … done
 [2/4] O-uz4vkv Old app logs · Claude … done
 …
 Plan P-ZDHP applied: 4 done · 0 skipped · 0 failed
 Free space change (measured): +806 MB
 Expected: 825 MB
 Every step is in the audit log: macdoc log
```
| Command | Does |
|---|---|
| `macdoc apply <P-id>` | Shows the dry run again and asks you to type the plan's code. RISKY items, and every item of a plan Claude proposed, are also confirmed one by one (default No). Needs a real Terminal: piped input is refused, and there's no `--yes` |
| `macdoc quarantine` / `quarantine list` | Anything apply moved aside but couldn't delete (normally empty), how long each entry stays restorable, and what has expired |
| `macdoc quarantine restore <Q-id>` | Moves a folder back where it was. It never overwrites: if something now exists there, it stops |
| `macdoc quarantine purge --expired` · `purge <Q-id…>` | Permanently deletes entries, after you type `purge` in Terminal. Nothing is ever purged automatically |
| `macdoc log` | Everything macdoc changed: plans, applies (each item done, skipped or failed, with the reason), restores and purges |

Right before touching each item, apply checks it again: it's still the same folder (same device and inode), its path still resolves the same way, the allowlist and protected-path rules still pass (with a freshly built policy), the preconditions hold (Xcode closed, emulator stopped, Docker running, …), and it holds no git repository or `.env` file. Anything that changed is skipped with a reason, and the rest continue. Folders are moved with `renameatx_np` relative to the opened parent folder, so a symlink swapped in at the last moment can't redirect the move. Native commands run with a fixed argv and no shell, and a tool identifier (a simulator, AVD or model) must still be in the tool's own listing.

### Use it from Claude (`macdoc mcp`)
Connect once:
```bash
claude mcp add macdoc -- macdoc mcp       # Claude Code
```
For Claude Desktop, add to `claude_desktop_config.json` under `"mcpServers"`: `"macdoc": {"command": "/Users/you/.local/bin/macdoc", "args": ["mcp"]}`.

Then ask things like "why is my Mac slow?" or "what can I safely clean?". Claude can read the overview, suggestions, explanations, projects, the memory diagnosis and growth history, start a read-only rescan, and **propose** a plan. It gets back a plan ID and the command for you, e.g. `macdoc apply P-7F3K`; you run it in Terminal and confirm each item. Claude can't apply, delete, move or restore anything, pass a path, run a command or change settings. Claude can have at most 5 plans open and make 10 every 10 minutes. A scan started from Claude has the Claude app's file permissions, so it may see less than one from Terminal; when it does, Claude is told to suggest `macdoc doctor scan` in Terminal.

## Quick start

Requirements: macOS on Apple Silicon or Intel, and Go 1.27 or newer (`brew install go`). Optional: `golangci-lint` for `make lint`.

```bash
git clone https://github.com/sagnik26/macdoc.git macdoc && cd macdoc
make install               # builds and installs macdoc into ~/.local/bin
                           # (make sure ~/.local/bin is on your PATH; or: make install BINDIR=/usr/local/bin)
macdoc                     # interactive dashboard (scans automatically the first time)
macdoc doctor              # the short summary, printed
macdoc doctor scan         # fresh scan + the full report
macdoc doctor explain <id> # details for one suggestion
```
There are no released binaries yet (see [docs/RELEASING.md](docs/RELEASING.md) for how they will be built and checked). After changing the code, run `make install` again to update the installed `macdoc`. Running `macdoc version` shows the commit it was built from.

Tab completion for zsh (bash and fish work the same way with `macdoc completion bash|fish`):
```bash
mkdir -p ~/.zfunc && macdoc completion zsh > ~/.zfunc/_macdoc
# then in ~/.zshrc, before compinit: fpath=(~/.zfunc $fpath)
```

Settings: `macdoc config` shows them. `macdoc config protect ~/work/client` makes macdoc never suggest or remove anything in that folder; `macdoc config set plan_minutes 60` keeps plans valid longer. Settings can only make macdoc stricter or change timings.

**For complete results,** give your terminal app **Full Disk Access** (System Settings › Privacy & Security › Full Disk Access), then restart the terminal. Without it, macdoc reports how much space it couldn't see (about 36 GB on the dev Mac), and lists the locations.

Scan results live in `~/Library/Application Support/macdoc/state.db` (SQLite, mode 0600). The 10 newest scans are kept, plus one per week for 13 weeks (for `trend`).

### Known limitation: a suggestion's size is a floor, and the real gain can be higher

On APFS, a file that is a clone of another shares its blocks with it, so deleting it frees less than its size (up to three times less in the experiment in `docs/experiments/S1-apfs-private-size`). For a suggestion made from the scan, macdoc therefore shows the space removing it is certain to free: the sum of the kernel's own per-file figure for the bytes a delete releases. That never overstates, but it can understate. A folder whose files are all clones of each other shows 0 (the space is released only when the last copy goes), and a file with more than one hard link counts as 0 because the kernel reports its full size for every link. Where a volume cannot report that figure (HFS+, exFAT), macdoc falls back to allocated blocks, the same thing `du` reports, as before. Sizes a tool reports (Docker, Homebrew, simctl) are shown as the tool gives them. `macdoc scan` and `doctor storage` still show allocated blocks, since they describe what is on disk. A range for sets of clones is not built yet ([docs/adr/0005](docs/adr/0005-size-by-allocated-blocks.md)). This affects what you decide to remove, not whether removal is safe.

---

## What it is

macdoc is a **storage doctor for developer Macs, with a built-in explanation of why the Mac is slow.** It:

- **Diagnoses** the machine: hardware, APFS container usage, purgeable space, local Time Machine snapshots, swap and memory pressure.
- **Explains where storage went**, including the opaque "System Data" bucket, broken down by real causes: Xcode, Android, Node/React Native, Docker, Python, local AI models, Homebrew and stale projects.
- **Finds reclaimable space.** Every item has an honest size (measured, tool-reported, estimated or unknown), a risk level (SAFE / CAUTION / RISKY / ADVISE), its idle time, and what it costs to get back.
- **Plans cleanups as dry-runs first.** Nothing is removed until you review a sealed plan and confirm it (typed code in Terminal, or a second Enter in the dashboard).
- **Cleans safely.** It uses each tool's own cleanup command, or moves a folder through macdoc's quarantine and deletes it there, so the space is freed at once and a crash never leaves a half-deleted folder. Every step is in an audit log.
- **Connects to Claude** through a local MCP server (`macdoc mcp`). Claude can diagnose and *propose* plans, and only you can apply them.

It is **not** a "Mac cleaner" in the usual sense. There's no background daemon, no automatic deletion, no "RAM boosting", no killing processes, and no fake health score.

## Why we need it

The target machine is an **8 GB MacBook used for full-stack and mobile development** (Xcode, Android Studio, React Native, Node.js, Homebrew, Docker, npm/pnpm/yarn, Python, local AI models). Machines like this fall into a predictable spiral:

```
Low RAM ──▶ heavy swap ──▶ swap files grow on disk ──▶ free space shrinks
   ▲                                                         │
   └──── macOS hangs, builds fail, updates won't install ◀───┘
```

Developer tooling quietly eats tens of gigabytes:

| Source | Typical size |
|---|---|
| Docker Desktop disk image, build cache, images | 20–60 GB |
| Xcode DerivedData, simulator runtimes, DeviceSupport, Archives | 20–80 GB |
| Android emulators, system images, old SDKs, Gradle caches | 15–50 GB |
| Local AI models (Ollama, Hugging Face, LM Studio) | 10–100 GB |
| `node_modules`, Pods and build folders in idle projects | 5–40 GB |
| Package-manager caches (npm, pnpm, yarn, pip, uv, CocoaPods, Homebrew) | 5–30 GB |
| Local snapshots and swap, shown only as "System Data" | 5–30 GB |

**Existing tools don't solve this well:**
- **Finder's storage view** shows opaque buckets with no explanation.
- **Generic cleaners** don't understand developer tooling, delete blindly, and use scare tactics.
- **Manual cleanup** means remembering dozens of paths, and it's easy to delete something irreplaceable (Xcode Archives with dSYMs, Docker volumes).

## Why we're building it this way

1. **Developer-aware.** DerivedData is regenerable, but Archives hold irreplaceable dSYMs. A system image that no AVD uses is dead weight.
2. **Honest numbers.** A suggestion shows the space removing it is certain to free (never more); disk usage shows allocated bytes, not logical size, with hard links counted once. Every number says where it came from, and unknown is never shown as 0.
3. **Safety is the product.** Read-only by default. Actions come only from a fixed recipe catalog with exact command allow-lists, and never from arbitrary paths. There's a dry-run before anything happens, confirmation by a person at the terminal, a crash-safe quarantine step, and an audit log.
4. **Built for low-RAM hardware.** Scans run in the macOS background priority band with bounded memory.
5. **Ready for Claude without handing over control.** CI proves the MCP and engine code can never reach the deletion code.
6. **Local-first and private.** No network calls from macdoc, no telemetry, no account.

---

## Progress

| Phase | What | Status |
|---|---|---|
| 0 | Foundations: path-safety layer, sandbox mode, import and forbidden-call guards, lint, CI | ✅ done (red-teamed, 9 findings fixed) |
| 1 | System probe → `doctor` | ✅ done |
| 2 | Full-crawl scanner + SQLite store → `scan` | ✅ done |
| 3 | 46 recipes + classifier; `macdoc doctor` short summary, `macdoc doctor scan` full report, single sections (`health`, `space`, `wins`, `explain`, `dev`) | ✅ done (red-teamed, 12 findings fixed, reviewed) |
| 3.5 | Terminal UI: the `macdoc` dashboard (Bubble Tea) and colored output (Lip Gloss), with truecolor → 256 → 16 fallback | ✅ done |
| 4 | Project awareness: `doctor projects` and the Projects tab; idle time from git commits, file changes and builds; uncommitted changes raise the risk; Pods, RN/Android build folders, `.next`; DerivedData mapped to its project | ✅ done |
| 5 | Planner: sealed, expiring dry-run plans → `macdoc plan`, plus marking and planning in the dashboard | ✅ done |
| 6 | Executor + quarantine + audit log → `apply`, `quarantine`, `log` (the first phase that can change files) | ✅ done |
| 7 | `slow`, `trend`, frozen JSON schemas, `config` (protected folders, plan and command timeouts), completions, a 10 MB binary → **v0.1 (MVP B)** | built; dogfood week ended 2026-11-04, not signed off (0.88 of 10 GB reclaimed) |
| 8 | MCP server for Claude: 11 read and propose-only tools, `macdoc mcp` | ✅ built |
| 9 | Claude handoff and hardening: proposal time in `apply`, at most 5 open Claude plans and 10 per 10 minutes, rescan guidance, Claude-app permission warnings | ✅ built |
| 10 | Optional daily read-only scans (`macdoc schedule enable\|disable`), pre-marked SAFE suggestions in the dashboard, slowness tips (`slow --tips`) | ✅ built |
| 11 | Open-source readiness: Google Go style and complexity gates, tests found missing by mutation testing, a threat model and decision records, a release pipeline with reproducible archives, experiments that measured the design | 🟡 in progress: [plan.md](plan.md) has the plan and what is done; nothing is tagged or signed yet |

Each completed phase has a checkpoint in [`docs/checkpoints/`](docs/checkpoints/), with real-Mac measurements, the tasks run, and the safety findings.

## Safety model, as implemented so far

- **Path safety** (`internal/safety`):
  - Canonicalization that rejects `..` and trailing `/`, resolves symlinked parents, normalizes Unicode and ignores case.
  - Protected paths: system roots, `~/Documents`/`Desktop`/…, iCloud, Mail, Keychains, `.ssh`, `.git`, `.env*`, SQLite files and their sidecars, Containers/`com.apple.*`, parents of any protected folder, and other users' homes.
  - **No cleanup target can be outside `$HOME`.**
  - 99.5% coverage, fuzz-tested.
- **Your protected folders** (`macdoc config protect <folder>`, `internal/config`): added on top of the built-in list, never replacing it. Suggestions, planning and the executor's last check all honor them, and an unreadable settings file stops those with an error instead of silently dropping a folder.
- **Recipe lint** (`internal/recipes/load.go`), applied to every recipe at load time:
  - Native and dry-run commands must **exactly match an allow-list** of 18 and 3 commands.
  - Allow patterns must be `~`-relative and at least 2 deep, with no `..`, and can't reach protected folders.
  - Preconditions use a strict character set.
- **Classifier:**
  - Drops a finding if the tool is missing, the identifier is unsafe or reserved (`all`, …), it fails `CheckTarget`, it was used too recently, or its age is unknown when a minimum idle time applies.
  - Raises risk for anything changed in the last 7 days.
  - Merges overlapping and duplicate findings.
- **Detectors are read-only.** File reads go through a regular-file, non-blocking, size-capped reader. External commands go only through `platform.Runner`. Detectors never run package managers to discover things. (Querying pnpm/yarn once triggered Corepack downloads, so that's now forbidden.)
- **Executor** (`internal/executor`, the only package that moves, deletes or runs mutating commands):
  - Acts only on a plan that passes `planner.Verify`, after you type its code at a real terminal. Marking the plan applied is the lock, so a plan runs at most once, even after a crash.
  - Re-checks every item right before acting; any change means the item is skipped and audited, never "fixed up".
  - Folders go to `~/Library/Application Support/macdoc/quarantine/Q-XXXX/` (private, excluded from Spotlight and Time Machine), with a manifest written before the move, and are then purged right away through the same checks as `quarantine purge` (since 2026-10-28). A crash at any step leaves a restorable entry and a readable audit log (tested by killing a child process mid-move). If the purge is refused, the entry stays in quarantine and `macdoc quarantine` lists it.
  - The only recursive delete is purging a quarantine entry that has macdoc's own record and manifest; a test pins every delete, rename and exec call site in the package.
  - Every executor test runs under `MACDOC_SANDBOX`, and real native commands are refused in sandbox mode.
- **Names from the disk are shown cleaned.** A folder name can hold terminal escapes or invisible characters. Human output and the dashboard drop them before printing, so a name can't redraw what you confirm, and MCP output drops them before Claude reads them. Planning and the executor always use the names as found.
- **CI guards:** `importcheck` (MCP and the engine can't import the executor) and `forbidcheck` (no delete, rename, exec or shell calls outside the executor, aliased imports included). Both are verified against planted violations.

Full details: [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md).

---

## Architecture

```
macdoc (one Go binary)
  cmd/macdoc            entry point
  internal/cli          commands and rendering: the dashboard, `doctor` and its sections, plan, apply,
                        quarantine, log, config, version
  internal/engine       UI-agnostic core: Overview, Doctor, Slow, Trend, Scan, Opportunities (classifier/
                        ranking), Storage, Projects, plans, audit and quarantine views
  internal/probe        hardware, memory, swap, processes, APFS, snapshots (read-only, via Runner)
  internal/scanner      parallel full crawl: allocated bytes, hard links once, bounded memory, background QoS
  internal/store        SQLite state (scans, nodes, unmeasured paths, plans, quarantine, vitals), pure Go
  internal/recipes      recipe schema, loader + safety lint, detectors, catalog/*.yaml (49 recipes)
  internal/projects     project discovery, idle time, git state (read-only git)
  internal/planner      sealed, expiring dry-run plans (IDs only, never paths) and Verify
  internal/precond      preconditions: app not running, emulator stopped, Docker reachable, …
  internal/audit        append-only JSONL audit log
  internal/quarantine   quarantine manifests and entry states (read-only)
  internal/config       settings file: timings and extra protected folders (add-only)
  internal/safety       canonicalization + protected-path policy
  internal/pathfold     the one definition of "the same name" (case and Unicode folding) used by every path comparison
  internal/fsread       capped, non-blocking regular-file reader for detectors
  internal/textsafe     cleans names from the disk before you or Claude see them (escapes, invisible characters)
  internal/sandbox      MACDOC_SANDBOX for executor tests (fails closed)
  internal/platform     read-only command Runner (+ fake), darwin cgo shim for purgeable space
  internal/model        shared types: Opportunity, Plan, Node, Envelope …
  internal/executor     the ONLY package allowed to delete, move or run mutating commands
  internal/mcp          MCP server (`macdoc mcp`): read + propose plans; may never import the executor
  internal/testutil     fstree fixture builder for fake home directories
  tools/forbidcheck     AST guard against delete/exec/shell calls outside the executor
  scripts/              import, forbidden-call and threat-model guards; shape.sh (the shape of a scan, no paths)
  schema/*.json         frozen JSON Schema for every --json command (contract-tested)
```

The CLI and the MCP server are two front doors over the same engine. See [docs/BUILD_STRATEGY.md](docs/BUILD_STRATEGY.md).

## Development

```bash
make build        # ./bin/macdoc
make install      # build + copy to ~/.local/bin (BINDIR=… to change)
make test         # go test -race ./...
make cover        # coverage across packages (tests live in internal/<pkg>/tests/)
make lint         # golangci-lint (0 issues required)
make importcheck  # MCP/engine must not reach the executor
make forbidcheck  # no delete/exec/shell outside internal/executor
make threatcheck  # every test the threat model and decision records name still exists
make vulncheck    # govulncheck (known vulnerabilities in what the module calls)
make check        # test, lint, importcheck, forbidcheck and threatcheck
make fuzz         # fuzz the path-safety layer
```

New code is held to Google Go style (doc comments on exported names, naming) and to complexity limits (cognitive 25, cyclomatic 15). The older code is checked only where a change touches it, relative to `origin/main`, while the backlog is cleared. CI also runs `make vulncheck`; a tag builds the release archives (see [docs/RELEASING.md](docs/RELEASING.md)).

Rules for contributors are in [CONTRIBUTING.md](CONTRIBUTING.md). The safety rules are in [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md) and the decision records in [docs/adr/](docs/adr/README.md).

### How it's being built
A **tech-lead Claude session** writes the contracts, and delegates bounded tasks to **headless worker Claude sessions** that run in visible tmux panes, each in its own git worktree. The tech lead verifies every task independently (tests, lint, guards, real-Mac runs, planted violations). **Red-team reviewers** attack the safety-critical code, and every finding becomes a test. Workers can only write inside their worktree, use a narrow command allowlist, and keep the Mac awake while running. This ran for Phases 0–3; from Phase 4 on, the tech-lead session built each phase itself and recorded it in a checkpoint. Details are in [docs/ORCHESTRATION.md](docs/ORCHESTRATION.md). Every task brief is in [`docs/tasks/`](docs/tasks/), and every report in [`docs/tasks/reports/`](docs/tasks/reports/).

## Documents

| File | What's in it |
|---|---|
| [docs/DEVELOPMENT_PLAN.md](docs/DEVELOPMENT_PLAN.md) | Architecture, phases, tasks, acceptance criteria, testing, risks, carry-overs |
| [docs/RELEASING.md](docs/RELEASING.md) | Cutting a release, a dry run, and how to rebuild a tag and compare hashes |
| [docs/adr/](docs/adr/README.md) | Decision records: why approval is terminal-only, IDs not paths, one mutating package, delete-after-move, sizing by blocks, the test layout |
| [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) | Assets, adversaries, trust boundaries and, for each defense, the tests that fail without it (checked by `make threatcheck`) |
| [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md) | Risk classes, protected paths, the plan → confirm → apply → verify lifecycle, quarantine, audit, threat model |
| [docs/RECIPE_CATALOG.md](docs/RECIPE_CATALOG.md) | Recipe schema, catalog intent, allowed native commands, how to add a recipe |
| [docs/CLI_UX.md](docs/CLI_UX.md) | Commands, report mockups, tone rules, JSON contract |
| [docs/MCP_DESIGN.md](docs/MCP_DESIGN.md) | Phase 8–9: MCP tools, the Claude-to-terminal handoff, prompt-injection hardening |
| [docs/BUILD_STRATEGY.md](docs/BUILD_STRATEGY.md) | One engine with two front doors, and the safety-first build order |
| [docs/ORCHESTRATION.md](docs/ORCHESTRATION.md) | Tech lead + worker agents, permissions, verification, checkpoints |
| [plan.md](plan.md) | The engineering plan for open-source readiness, with a progress log of what is done and what is not |
| [docs/experiments/](docs/experiments/) | Reproducible experiments with their code and results: APFS sizing, mutation testing, test-only exports, the node-prefix query |
| [docs/checkpoints/](docs/checkpoints/) | Per-phase results measured on the real Mac |
| [docs/tasks/](docs/tasks/), [docs/tasks/reports/](docs/tasks/reports/) | Task briefs and archived worker/reviewer reports |

## Decisions locked

| Topic | Decision |
|---|---|
| Scope | MVP B (diagnosis, dry-run planner, terminal-confirmed apply, audit log), then C (Claude via local MCP) as a pure addition |
| Default mode | Read-only. Read-only commands never delete anything, including expired quarantine |
| Removal | The owning tool's native command first (from an exact allow-list). Otherwise a move into macdoc's quarantine, deleted right away so the space is freed (2026-10-28; originally held for 7 days) |
| Targets | Never outside `$HOME` by path. Anything outside home is only ever touched through native tool commands |
| Scanning | Full crawl of the Data volume, background priority, streaming aggregation, bounded memory |
| Language | Go. Single binary, with an optional cgo shim for purgeable space |
| Audience | Personal first, open-source later. Recipes are data-driven |
| Claude boundary | Claude can diagnose and propose plans. Only a human at a real terminal can apply them. The MCP code can't reach the deletion code (CI import guard), and no MCP tool acts on the Mac |
