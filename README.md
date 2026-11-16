<div align="center">
  <h1>macdoc</h1>
  <p><b>Find out why your Mac is full and slow, clean up, uninstall, analyze disk and monitor your Mac. Free open-source CLI for developer Macs.</b></p>
  <a href="https://github.com/sagnik26/macdoc/stargazers"><img src="https://img.shields.io/github/stars/sagnik26/macdoc?style=flat-square" alt="Stars"></a>
  <a href="https://github.com/sagnik26/macdoc/releases"><img src="https://img.shields.io/github/v/release/sagnik26/macdoc?label=version&style=flat-square" alt="Version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg?style=flat-square" alt="License"></a>
  <a href="https://github.com/sagnik26/macdoc/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/sagnik26/macdoc/ci.yml?branch=main&label=CI&style=flat-square" alt="CI"></a>
  <a href="https://github.com/sagnik26/macdoc/commits"><img src="https://img.shields.io/github/commit-activity/m/sagnik26/macdoc?style=flat-square" alt="Commits"></a>
</div>

<p align="center">
<pre>
 ◆ macdoc  MacBookAir10,1 · M1                             51.2 GB free · scan 6m ago · asks before removing
  1 Overview   2 Suggestions   3 Toolchains   4 Projects   5 Space   6 Health
 SAFE 825 MB  ·  CAUTION 553 MB  ·  ADVICE 48.9 GB   item 1 of 12
 ╭───┬──────────┬─────────┬─────────┬────────────────────────────────┬──────────────────────────────╮
 │   │ ID       │    Size │ Risk    │ Suggestion                     │ Restore                      │
 ├───┼──────────┼─────────┼─────────┼────────────────────────────────┼──────────────────────────────┤
 │ ▶ │ O-gmyov5 │  394 MB │ SAFE    │ uv cache                       │ re-downloads on demand       │
 │   │ O-gg6lrb │  197 MB │ CAUTION │ Android build-tools 34.0.0     │ re-downloads on demand       │
 │   │ O-dkuv2w │ 33.7 GB │ ADVICE  │ Leftover iOS simulator runtim… │ Runtimes can be downloaded … │
 ╰───┴──────────┴─────────┴─────────┴────────────────────────────────┴──────────────────────────────╯
</pre>
</p>

> 💡 macdoc makes **no network calls and sends no telemetry**. It removes only what you approve at a real Terminal. Claude can propose plans through MCP, but can never apply one.

## Features

- **All-in-one CLI toolkit**: Combines a storage doctor, an app cleaner, a disk explorer, and a system monitor in one terminal command
- **Deep clean**: Finds caches, logs, build artifacts, installers, and leftovers from uninstalled apps, with an honest size and a restore cost for each
- **Smart uninstaller**: Moves apps aside and finds the settings, caches, and background items they left behind
- **Disk analyzer**: Shows where the space is inside any folder, with no rescan
- **System maintenance**: Flushes DNS, frees inactive memory, and resets Quick Look through the system password prompt
- **Live monitoring**: Shows real-time CPU, memory, disk I/O, network traffic, battery, and a health score with the reasons

## Quick Start

macdoc supports both Intel and Apple Silicon Macs.

**Install via Homebrew**

```bash
brew install sagnik26/macdoc/macdoc
```

**Or via script**

```bash
curl -fsSL https://raw.githubusercontent.com/sagnik26/macdoc/main/install.sh | sh
```

The script checks the download's sha256 before installing to `~/.local/bin`, and never uses `sudo`.

**With an AI agent**

Tell your agent:

> Install macdoc for me by reading https://raw.githubusercontent.com/sagnik26/macdoc/main/llms.txt

Or give it the skill in [`skills/macdoc`](skills/macdoc/SKILL.md). With it, Claude Code and other agents run read-only checks and propose plans, and leave every apply to you in Terminal.

```bash
claude mcp add macdoc -- macdoc mcp
```

**Run**

```bash
macdoc                           # Interactive dashboard (type / for commands: /scan /reclaim /vitals /explore /retire /refresh)
macdoc reclaim                   # Lists cleanups by risk; at a Terminal, pick which risk types to clean
macdoc retire                    # Installed software and the data that belongs to it
macdoc refresh                   # Maintenance steps (flush DNS, free memory, reset Quick Look)
macdoc explore [path]            # Visual disk explorer (--growth, --projects)
macdoc vitals                    # Live system health dashboard (--memory: is memory slowing your Mac?)
macdoc scan                      # Fresh scan or last data (it asks), then the full report; --fresh / --last skip the question
macdoc apply <id…>               # Seal a plan, review it, and carry it out after you confirm in Terminal

# More: log, quarantine, config, schedule, mcp, completion, version
macdoc --help                    # Show help
```

**Preview safely**

```bash
macdoc reclaim                   # See what could be reclaimed; at a Terminal, tick what to clean, then Enter
macdoc apply --safe --dry-run    # Dry run of every SAFE suggestion (changes nothing)
macdoc apply O-ab23cd O-k3m9pq --dry-run   # Dry run of the suggestions you pick
macdoc apply --recipe apps.leftovers --dry-run
macdoc apply P-ZDHP              # Applies a sealed plan: shows it again, asks you to type its code
macdoc apply --list              # Pending plans
macdoc quarantine restore <id>   # Put a moved folder back
macdoc log --json                # Activity as JSON, with a run ID per command
```

<details>
<summary><strong>Other install options</strong></summary>

To install a specific release, set `MACDOC_VERSION` to any tag from the [releases page](https://github.com/sagnik26/macdoc/releases), and `MACDOC_PREFIX` to install somewhere other than `~/.local`:

```bash
curl -fsSL https://raw.githubusercontent.com/sagnik26/macdoc/main/install.sh | MACDOC_VERSION=v0.1.0 MACDOC_PREFIX="$HOME/.local" sh
```

**Build from source**

You need Go 1.27.4 or newer:

```bash
git clone https://github.com/sagnik26/macdoc.git && cd macdoc
make install               # installs into ~/.local/bin (BINDIR=/usr/local/bin to change)
```

**Shell completion**

```bash
mkdir -p ~/.zfunc && macdoc completion zsh > ~/.zfunc/_macdoc
# then in ~/.zshrc, before compinit: fpath=(~/.zfunc $fpath)
```

`bash` and `fish` work the same way. macdoc is not notarized yet: if macOS blocks it, run `xattr -d com.apple.quarantine ~/.local/bin/macdoc`.

</details>

macdoc is only distributed through Homebrew, this repository and its [GitHub releases](https://github.com/sagnik26/macdoc/releases).

## Safety

macdoc can remove files, so it validates paths, protects system and personal locations, and asks for confirmation at a real Terminal before anything is removed.

- `retire`, `refresh`, `explore`, `vitals` and `scan` are read-only, and `reclaim` is read-only unless you choose risk types and press Enter at its prompt in a real Terminal (SAFE only by default; RISKY keeps apply's confirmations), and `apply --dry-run` only seals a plan: review it before you apply it. `apply` always shows the plan and asks you to confirm in Terminal
- Plans and tools take suggestion IDs, never paths, and there is no `--yes`: only a person at the Terminal approves a removal
- Every action comes from a fixed recipe catalog with exact command allow-lists: no shell, no `docker system prune`, no `--volumes`
- Each item is checked again right before it is touched; anything that changed is skipped and recorded, never "fixed up"
- Folders go to a private quarantine first and are deleted through the same checks; `macdoc quarantine restore` puts one back while it is there
- System folders, `~/Documents`, `~/Desktop`, iCloud, Mail, Keychains, `.ssh`, `.git`, `.env*` and database files are never touched. The one exception is an app bundle directly in `/Applications`, with its own confirmation
- Administrator steps (flush DNS, free memory) run only through the system password prompt, from an exact list, and never from a plan Claude proposed
- Every change is in the audit log (`macdoc log`) and the quarantine

Review [SECURITY.md](SECURITY.md), [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md) and [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) for reporting guidance, safety boundaries, and what each defence is tested by.

## Configuration

macdoc runs without configuration. Settings can only make it stricter or change timings. `macdoc config` shows them.

- `macdoc config protect ~/work/client`: never suggest or remove anything in that folder
- `macdoc config set plan_minutes 60`: keep plans valid longer (default 30)
- `"excluded_recipes": ["maintenance.*"]` in the settings file turns off tasks by ID, or a whole family with a trailing `.*`

These environment variables adapt macdoc to slow disks or stalled network mounts; raise a timeout only when macdoc reports that a command timed out:

- `MACDOC_TIMEOUT` (10): seconds allowed for each read-only command (1 to 600)
- `MACDOC_TIMEOUT_<COMMAND>`: the same for one command, for example `MACDOC_TIMEOUT_TOP=30`

```bash
MACDOC_TIMEOUT=30 macdoc scan
```

For complete results, give your terminal app **Full Disk Access** (System Settings › Privacy & Security), then restart it. Without it, macdoc reports how much space it couldn't see.

## Features in Detail

The examples below are shortened. Available items, sizes, and restore costs depend on your Mac.

### Reclaim

`macdoc reclaim` is the one-step cleaner. At a real Terminal it lists everything by risk, then asks "What do you want to clean?" with a row each for SAFE, CAUTION, RISKY and ADVICE (item count, size, a short note). Tick the risk types to clean in a checkbox list (↑↓ move, space ticks, `a` ticks all, Enter starts the cleanup, Esc cancels; SAFE starts ticked and ADVICE can never be ticked). Where there is no raw terminal it asks you to type `safe`, `caution`, `risky` or `all` instead. CAUTION is cleaned only when you name it. RISKY is cleaned only when you name it, and then keeps `apply`'s full confirmation (the plan is shown, each RISKY item asks y/N, and you type the plan's code), so one Enter never approves RISKY. ADVICE rows are shown but can never be chosen, and `all` never includes them. Every cleanup runs with every re-check, quarantine step and audit entry. Piped, with `--json`, `--quiet` or `--dry-run`, it only lists. `macdoc reclaim <toolchain>` limits the list and the cleaning; `--tier X` skips the question and cleans only that tier on Enter; `--toolchains` and `--categories` are read-only reports. Administrator steps and Claude-proposed plans are never cleaned this way, and there is no `--yes`. `macdoc apply` remains for specific IDs, other tiers, and sealed or Claude-proposed plans: it seals what you pick as a plan, shows the dry run, and removes it after you type the plan's code. Add `--dry-run` to only seal and show it; `macdoc apply P-ZDHP` applies a plan that already exists, including one Claude proposed.

```text
$ macdoc reclaim

 Free & safe ──────────────────────────────────────────────────── 6.7 MB
  O-qgqse7    6.7 MB  Homebrew old versions and downloads           re-downloads on demand
                      → brew cleanup --prune=all

 Worth considering ──────────────────────────────────── 279 MB + unknown
  O-heri2j    161 MB  Node.js v20.20.2 (nvm)                        re-downloads on demand
  O-yzbseg    3.2 MB  Data left by com.example.chat (HTTPStorages)  The app isn't installed
                      4 × Background item whose program is gone

 Your call (advice only) ──────────────────────────────────────── 8.8 GB
  O-okj5vt    8.8 GB  Leftover iOS simulator runtime downloads      Runtimes can be downloaded again

$ macdoc apply --safe --dry-run
Plan P-ZDHP · 1 item(s) · 6.7 MB · expires in 30 min · made by you

 Nothing has been changed.
 To apply it: macdoc apply P-ZDHP · expires 15:41

$ macdoc apply P-ZDHP
 Type "ZDHP" to apply 1 item(s), 6.7 MB (SAFE: 1): ZDHP
 Plan P-ZDHP applied: 1 done · 0 skipped · 0 failed
 Free space change (measured): +6.4 MB
```

CAUTION and RISKY items, and every item of a plan Claude proposed, are confirmed one by one. Piped input is refused.

`macdoc reclaim` also finds installer files (`.dmg`, `.pkg`, `.mpkg`, `.iso`, `.xip`) and archives in Downloads that haven't changed for a month, and suggests them as CAUTION items that can be downloaded again. Other large downloads, which may be the only copy, are advice only.

<details>
<summary><strong>Installer example</strong></summary>

```text
$ macdoc reclaim

 Worth considering ──────────────────────────────────── 279 MB + unknown
  O-puaajd    111 MB  Old installer or archive                      re-downloads on demand
```

</details>

### Retire

Apps appear as suggestions, together with the settings, caches, and background items an uninstalled app left behind. `macdoc retire` shows each program with the data folders that belong to it and the cleanups that cover it.

An app is moved aside only when it is not running, and each one needs its own confirmation. Apple's apps and Mac App Store apps are never offered. The app's settings and caches are kept until nothing installed owns them, and then macdoc offers them as leftovers.

```text
$ macdoc reclaim --tier risky

 Needs care ─────────────────────────────────────────── 4.4 GB + unknown
  O-hzyl53   1.5 GB  Google Chrome (app)               re-downloads on demand
  O-ywvjll   1.1 GB  Cursor (app)                       re-downloads on demand
  O-chlpcv   549 MB  Wispr Flow (app)                   re-downloads on demand
```

`macdoc retire` also reports which universal apps still carry an Intel slice and about how many bytes it takes. This is advice only: macdoc doesn't remove the slice, because re-signing a thinned Developer-ID app makes Gatekeeper reject it ([ADR 0010](docs/adr/0010-slimming-universal-apps.md)).

### Refresh

`macdoc refresh` lists maintenance steps you can ask for: flush the DNS cache, restart the DNS responder, free inactive memory, and reset Quick Look. They are planned and applied through `macdoc apply`, like any other suggestion. Steps that need administrator rights say so in the dry run, and macOS asks for your password in Terminal; macdoc never sees it.

```text
$ macdoc refresh

  O-anezgn  Reset the Quick Look cache
  O-pauohd  Restart the Quick Look server
  O-6jyde4  Flush the DNS cache  needs your Mac password
  O-evcipg  Restart the DNS responder  needs your Mac password
  O-p5h3jz  Free inactive memory  needs your Mac password

$ macdoc apply O-6jyde4
 CAUTION ────────────────────────────────────────────────────────── 0 B
  O-6jyde4  Flush the DNS cache
            → runs dscacheutil -flushcache
            Needs administrator rights: macOS asks for your password in Terminal.
 Nothing has been changed.
```

### Explore

`macdoc explore [path]` shows where the space is inside one folder from the latest scan, biggest items first, with a share bar and a "smaller items" line so the sizes add up. It never rescans and never takes a path as a cleanup target. A size marked `partial` means some paths couldn't be read.

```text
$ macdoc explore

 /Users/you · 53.5 GB · partial
  1 path below could not be read (grant Full Disk Access and rescan), so sizes here are lower bounds
 ╭───────────────┬─────────┬──────────────────┬───────╮
 │ Name          │    Size │ Share            │     % │
 ├───────────────┼─────────┼──────────────────┼───────┤
 │ Library/      │ 43.7 GB │ █████████████░░░ │ 81.7% │
 │ .local/       │  2.4 GB │ █░░░░░░░░░░░░░░░ │  4.4% │
 │ Desktop/      │  1.6 GB │ ░░░░░░░░░░░░░░░░ │  2.9% │
 │ Downloads/    │  729 MB │ ░░░░░░░░░░░░░░░░ │  1.4% │
 ╰───────────────┴─────────┴──────────────────┴───────╯
```

In the dashboard, `/explore` (or `enter` on the Space tab) opens the explorer as a folder tree: `↑↓` move, `→` or `enter` opens a folder in place, `←` closes it, `backspace` goes up one folder.

`macdoc explore --projects` finds rebuildable project artifacts such as `node_modules`, Pods, iOS and Android builds, `.venv`, `.next`, and DerivedData. It groups them by project with how long the project has been idle and its git state. Projects with uncommitted changes raise the risk, and projects in protected folders are listed but never touched.

<details>
<summary><strong>Projects example</strong></summary>

```text
$ macdoc explore --projects

 Projects ────────────────────────────────────────────────── 1.3 GB in 5
  ~/work/backend · python
    idle 11 days (file change) · 465 MB · git clean
    .venv 465 MB
  ~/work/release-tool · node
    idle 13 days (file change) · 416 MB · git clean
    node_modules 416 MB
```

</details>

`macdoc explore --growth [--days 30]` shows what grew since an earlier scan, per well-known area and by folder.

### Vitals

`macdoc vitals` is a read-only dashboard for system pressure, disk activity, network traffic, power, and processes, with a 0 to 100 health score and the reasons it is lower.

```text
$ macdoc vitals

 Health 80 / 100 · MacBookAir10,1 M1
  -10 memory pressure is warn
  -10 swap use is 120% of RAM

 Now
  CPU      ██████░░░░░░░░░░░░░░  31.5%   load 4.57 / 3.94 / 5.08 (8 cores)
  Memory   ██████████████░░░░░░    70%   5.6 GB of 8.0 GB, pressure warn, swap 9.6 GB
  Disk     ██████████████░░░░░░  68.7%   76.8 GB free of 245 GB, 34.4 MB/s
  Network  down 674 B/s   up 252 B/s
  Power    █████████░░░░░░░░░░░    44%   on battery, discharging, 3:31 left · health 100%, 289 cycles

 Biggest memory users
 ╭───────────────┬─────────┬───────────╮
 │ Group         │  Memory │ Processes │
 ├───────────────┼─────────┼───────────┤
 │ Google Chrome │  2.0 GB │        71 │
 │ Cursor        │  427 MB │        18 │
 ╰───────────────┴─────────┴───────────╯
```

`/vitals` in the dashboard is a page of its own with the same live block, refreshed every 3 seconds while it is open, with a "Live · updated Ns ago" line and an arrow after each number for the way it moved. It needs no scan, and memory shows as a red alert only when it is what slows your Mac down.

<details>
<summary><strong>JSON, NDJSON, and process alerts</strong></summary>

- `macdoc vitals --json` returns a one-time status snapshot as JSON
- `macdoc vitals --watch --interval 2s --json` streams newline-delimited JSON
- `macdoc explore --json` returns a folder report with `status`: `complete`, `partial`, or `unavailable`
- `macdoc log --json` returns activity as JSON; entries carry a `run` ID shared by everything one command did
- Every command supports `--json`, and its shape is frozen in [`schema/`](schema/)

```text
$ macdoc vitals --json
{
  "score": 80,
  "reasons": ["-10 memory pressure is warn", "-10 swap use is 120% of RAM"],
  "cpu": { "usage_percent": 29.7, "load_1": 3.75 },
  "alerts": { "zombie_parents": [], "busy_processes": [] }
}
```

A section that couldn't be read (CPU, power, and so on) is left out with a note, never shown as zero. Process alerts are read-only: macdoc lists zombie parents and processes using 80% of a core or more, and never ends a process.

</details>

`macdoc vitals --memory [--days 7] [--tips]` answers "Is memory slowing my Mac down?" with a verdict, the biggest users against RAM, and swap over the window.

## Quick Launchers

<details>
<summary><strong>Raycast and Alfred setup</strong></summary>

The launchers only run the read-only `macdoc vitals` and `macdoc vitals --memory`. They never plan, apply, or delete.

**Raycast:**

1. Open **Raycast Settings > Extensions > Script Commands**.
2. Add the repository's `launchers/raycast` folder as a script directory.
3. Run **Reload Script Directories** in Raycast.

**Alfred:**

1. In an Alfred workflow add a Keyword input (for example `macdoc`) with an argument.
2. Connect a Run Script action: language `/bin/sh`, with argv, script `/path/to/launchers/alfred/macdoc.sh "$1"`.
3. Connect a Large Type or Copy to Clipboard output.

</details>

## Community

Thanks to everyone who helped build macdoc: see the [contributors](https://github.com/sagnik26/macdoc/graphs/contributors).

Rules for contributors are in [CONTRIBUTING.md](CONTRIBUTING.md). AI coding agents should read [AGENTS.md](AGENTS.md) first, because macdoc deletes files on people's Macs.

## Support

- If macdoc helped you, give it a star and [share it](https://github.com/sagnik26/macdoc)
- Open an [issue](https://github.com/sagnik26/macdoc/issues) for anything that looks wrong or unsafe
- For a security problem, follow [SECURITY.md](SECURITY.md) instead of opening a public issue
- Product requirements are in [docs/PRD.md](docs/PRD.md); design notes, the recipe catalog, and the decision records are in [docs/](docs/ARCHITECTURE.md)

## License

macdoc is open source under the MIT license; see [LICENSE](LICENSE).
