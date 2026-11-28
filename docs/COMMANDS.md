# macdoc commands

The short tour is in the [README](../README.md). This page is the per-command detail: what each command prints, which flags change it, and what is read-only. The examples are shortened. Available items, sizes, and restore costs depend on your Mac.

Bare `macdoc` opens the dashboard in an interactive terminal. Piped, with `--json` or `--quiet`, or when `TERM=dumb`, it prints the one-screen overview instead. In the dashboard, `/` lists `scan`, `reclaim`, `vitals`, `explore`, `retire`, and `refresh`.

| Command | Mode |
|---|---|
| `macdoc reclaim [toolchain]` | Lists suggestions. At a real Terminal it can clean the risk types you tick |
| `macdoc retire` | Read-only list of installed software and its data |
| `macdoc refresh` | Read-only list of maintenance steps |
| `macdoc explore [path]` | Read-only, from the latest scan |
| `macdoc vitals` | Read-only live status |
| `macdoc scan` | Reads the disk when you ask for a fresh scan, then prints the full report |
| `macdoc apply` | The command that removes. `--dry-run` only seals and shows a plan |

`log`, `quarantine`, `config`, `schedule`, `mcp`, `completion`, and `version` are under More in `macdoc --help`. Every command supports `--json`. The shapes are frozen in [`schema/`](../schema/).

## Preview safely

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

## Reclaim

`macdoc reclaim` is the one-step cleaner. At a real Terminal it lists everything by risk, then asks "What do you want to clean?" with a row each for SAFE, CAUTION and RISKY (item count, size, a short note; RISKY only when something is in it). Tick the risk types to clean in a checkbox list (↑↓ move, Enter or space ticks and unticks the highlighted type so several can be ticked, `a` ticks all, Esc cancels; SAFE starts ticked). A separate `Clean N items` button below the list is the only thing that starts the cleanup, and only Enter on it does. Where there is no raw terminal it asks you to type `safe`, `caution`, `risky` or `all` instead, and Enter alone means SAFE. CAUTION is cleaned only when you name it. RISKY is cleaned only when you name it, and then keeps `apply`'s full confirmation (the plan is shown, each RISKY item asks y/N, and you type the plan's code), so one Enter never approves RISKY. Advice-only items are listed above but have no row and can never be chosen. Every cleanup runs with every re-check, quarantine step and audit entry. Piped, with `--json`, `--quiet` or `--dry-run`, it only lists. `macdoc reclaim <toolchain>` limits the list and the cleaning; `--tier X` skips the question and cleans only that tier on Enter; `--toolchains` and `--categories` are read-only reports. Items that would free under 1 MB are counted in a note; `--small` lists them too. Administrator steps and Claude-proposed plans are never cleaned this way, and there is no `--yes`. `macdoc apply` remains for specific IDs, other tiers, and sealed or Claude-proposed plans: it seals what you pick as a plan, shows the dry run, and removes it after you type the plan's code. Add `--dry-run` to only seal and show it; `macdoc apply P-ZDHP` applies a plan that already exists, including one Claude proposed.

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

`macdoc reclaim` covers the same ground as a deep clean, without administrator rights: caches and logs (browsers, developer tools, desktop apps, sandboxed apps), cached iPhone and iPad restore files, Trash items (CAUTION, moved into macdoc's quarantine first so you can restore them), and the data apps left behind after they were removed. It never offers an app that is still installed (that is `macdoc retire`), and it only tells you about interrupted Time Machine backups, because removing them needs administrator rights.

`macdoc reclaim` also finds installer files (`.dmg`, `.pkg`, `.mpkg`, `.iso`, `.xip`) and archives in Downloads that haven't changed for a month, and suggests them as CAUTION items that can be downloaded again. Other large downloads, which may be the only copy, are advice only.

```text
$ macdoc reclaim

 Worth considering ──────────────────────────────────── 279 MB + unknown
  O-puaajd    111 MB  Old installer or archive                      re-downloads on demand
```

`macdoc reclaim --categories` is where space goes by category, with "System Data" decoded. `macdoc reclaim --toolchains` is reclaimable space per developer toolchain. `macdoc reclaim xcode` (or android, node, homebrew, docker, python, ai, system) is one toolchain.

## Retire

Apps appear as suggestions, together with the settings, caches, and background items an uninstalled app left behind. `macdoc retire` shows each program with the data folders that belong to it and the cleanups that cover it.

`macdoc reclaim` never offers an app that is still installed; it cleans caches and what removed apps left behind, and apps are removed only here. An app is moved aside only when it is not running, and each one needs its own confirmation. Apple's apps and Mac App Store apps are never offered. The app's settings and caches are kept until nothing installed owns them, and then macdoc offers them as leftovers.

```text
$ macdoc reclaim --tier risky

 Needs care ─────────────────────────────────────────── 4.4 GB + unknown
  O-hzyl53   1.5 GB  Google Chrome (app)               re-downloads on demand
  O-ywvjll   1.1 GB  Cursor (app)                       re-downloads on demand
  O-chlpcv   549 MB  Wispr Flow (app)                   re-downloads on demand
```

`macdoc retire` also reports which universal apps still carry an Intel slice and about how many bytes it takes. This is advice only: macdoc doesn't remove the slice, because re-signing a thinned Developer-ID app makes Gatekeeper reject it ([ADR 0010](adr/0010-slimming-universal-apps.md)).

## Refresh

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

## Explore

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

`macdoc explore --growth [--days 30]` shows what grew since an earlier scan, per well-known area and by folder.

## Vitals

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

- `macdoc vitals --json` returns a one-time status snapshot as JSON
- `macdoc vitals --watch --interval 2s --json` streams newline-delimited JSON
- `macdoc explore --json` returns a folder report with `status`: `complete`, `partial`, or `unavailable`
- `macdoc log --json` returns activity as JSON; entries carry a `run` ID shared by everything one command did
- Every command supports `--json`, and its shape is frozen in [`schema/`](../schema/)

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

`macdoc vitals --memory [--days 7] [--tips]` answers "Is memory slowing my Mac down?" with a verdict, the biggest users against RAM, and swap over the window.

## Scan

`macdoc scan` asks, in a terminal that already has a scan, whether to read the disk again or use the last data. `--fresh` and `--last` skip the question. Piped, `--json`, `--quiet`, and the scheduled scan always scan fresh. Other commands reuse a scan under 6 hours old.

## Apply, quarantine, log

`macdoc apply <O-id…>`, `--safe`, `--tier`, or `--recipe` seals a plan and shows the dry run. Without `--dry-run` it then asks you to confirm in Terminal, including typing the plan's code. `macdoc apply P-…` applies a plan that already exists, including one an assistant proposed. `macdoc apply` with no arguments applies nothing and prints pending plans and top suggestions. `--list` and `--show P-…` show plans. There is no `--yes`.

`macdoc quarantine restore <Q-id>` puts a moved folder back while it is still there. `macdoc quarantine purge` deletes expired entries after you type `purge` at a real Terminal.

`macdoc log` is the audit history. `--json` includes a `run` ID shared by everything one command did.

## Configuration

macdoc runs without configuration. Settings can only make it stricter or change timings. `macdoc config` shows them.

- `macdoc config protect ~/work/client`: never suggest or remove anything in that folder
- `macdoc config set plan_minutes 60`: keep plans valid longer (default 30)
- `macdoc config hide <id>` stops listing a kind of suggestion for good (an `O-…` ID from the list, or a recipe ID like `user.trash`); `macdoc config unhide <recipe id>` brings it back. In the dashboard, `x` on a suggestion does the same. It only hides: it never adds or enables anything, and the list's notes say how many items are hidden.
- `"excluded_recipes": ["maintenance.*"]` in the settings file does the same by hand, for a whole family with a trailing `.*`

These environment variables adapt macdoc to slow disks or stalled network mounts; raise a timeout only when macdoc reports that a command timed out:

- `MACDOC_TIMEOUT` (10): seconds allowed for each read-only command (1 to 600)
- `MACDOC_TIMEOUT_<COMMAND>`: the same for one command, for example `MACDOC_TIMEOUT_TOP=30`

```bash
MACDOC_TIMEOUT=30 macdoc scan
```

For complete results, give your terminal app **Full Disk Access** (System Settings › Privacy & Security), then restart it. Without it, macdoc reports how much space it couldn't see.

## Launchers

The launchers only run the read-only `macdoc vitals` and `macdoc vitals --memory`. They never plan, apply, or delete.

**Raycast:**

1. Open **Raycast Settings > Extensions > Script Commands**.
2. Add the repository's `launchers/raycast` folder as a script directory.
3. Run **Reload Script Directories** in Raycast.

The two scripts are titled `macdoc vitals` and `macdoc vitals memory`.

**Alfred:**

1. In an Alfred workflow add a Keyword input (for example `macdoc`) with an argument.
2. Connect a Run Script action: language `/bin/sh`, with argv, script `/path/to/launchers/alfred/macdoc.sh "$1"`.
3. Connect a Large Type or Copy to Clipboard output.

The script's argument `status` or `vitals` runs `macdoc vitals`. `slow` or `memory` runs `macdoc vitals --memory`. `status` and `slow` are the original keywords and still work.
