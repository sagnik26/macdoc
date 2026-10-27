# macdoc

> A safe, local-first macOS CLI that explains why a developer Mac is full and slow, and gets space back without surprises.

**Status:** in development. **Phases 0–3 of 11 are complete.** Diagnosis works end to end and is **read-only**: `macdoc` can't delete anything yet. · **Started:** 2026-10-26 · **Language:** Go 1.27 · `macdoc` is a working name.

---

## What works today

**One command:** `macdoc doctor` (or just `macdoc`) shows everything on one screen: disk and memory health, where your space is, and what you can reclaim, ranked by safety. It scans automatically the first time. After that it reuses the latest scan and tells you when it's over a day old.

```
$ macdoc
macdoc · MacBookAir10,1 (M1, 8.0 GB) · macOS 26.6.2 · scan 29m ago

Disk       ▕█████████████████████       ▏ 191 GB / 245 GB used   54.1 GB free  (+3.2 GB purgeable)
Memory     8.0 GB · pressure warn · 37% free · swap 3.4 GB used
Snapshots  0 local Time Machine snapshot(s)

What matters most
  1. ! 3.4 GB of swap in use.
  2. ! Memory pressure is elevated.
  3. · Google Chrome is using 1.7 GB of RAM.

Where your space is (Data volume, 127 GB measured)
  /Users 50.9 GB · /System 39.0 GB · /Library 19.0 GB · /opt 7.5 GB · /private 5.1 GB · 36.4 GB not visible (needs Full Disk Access)

Reclaimable                              1.4 GB actionable · 48.9 GB advice
  Free & safe           825 MB   Homebrew old versions and downloads 412 MB · uv cache 394 MB · 4 × Old app logs 19.6 MB
  Worth considering     553 MB   2 × Old Android build-tools 392 MB · Node.js v20.20.2 (nvm) 161 MB
  Your call            48.9 GB   2 × Leftover simulator runtime data 48.9 GB

More: macdoc doctor wins (every item + ID) · explain <id> · space · health · --scan
Nothing has been changed.
```

**Sections, for more detail:**

| Command | Shows |
|---|---|
| `macdoc doctor` / `macdoc` | Everything above (~7 s; ~55 s the first time, when it scans) |
| `macdoc doctor --scan` | Refresh the scan first, then show everything |
| `macdoc doctor health` | Disk, memory, swap, snapshots, all findings, top memory users grouped by app/dev tool |
| `macdoc doctor space [--large]` | Reclaimable space by category, plus **"System Data" decoded** (swap, purgeable, snapshots, temp files, simulators, the part the scan can't see). `--large` adds big items no recipe covers |
| `macdoc doctor wins [--all] [--tier …]` | Every suggestion with its ID, size, restore cost and exact command |
| `macdoc doctor explain <id>` | One suggestion in detail: what, why, risk, idle time, how to restore |
| `macdoc doctor dev [toolchain]` | Suggestions per toolchain: xcode, android, node, homebrew, docker, python, ai, system |
| `macdoc doctor scan` | **Rescan, then the full report:** the overview, scan details (where the space actually is, what couldn't be read), health in full (all findings, top apps), space and "System Data" decoded (plus big items no recipe covers), every suggestion with its ID, and per-toolchain totals. Full crawl at background priority, ~55 s, ~25 MB RAM. `--foreground` is faster, `--json` gives one document |

Every view supports `--json`, which outputs a versioned envelope with top-level warnings.

**Not yet:** `plan` (Phase 5), `apply` / quarantine (Phase 6), `slow` / `trend` (Phase 7), and the Claude MCP server (Phase 8).

## Quick start

Requirements: macOS on Apple Silicon or Intel, and Go 1.27 or newer (`brew install go`). Optional: `golangci-lint` for `make lint`.

```bash
git clone <this repo> macdoc && cd macdoc
make install               # builds and installs macdoc into ~/.local/bin
                           # (make sure ~/.local/bin is on your PATH; or: make install BINDIR=/usr/local/bin)
macdoc                     # everything: health, space, reclaimable (scans the first time)
macdoc doctor --scan       # refresh the scan first (short summary)
macdoc doctor scan         # rescan + the full report, every section
macdoc doctor wins         # every suggestion with its ID
macdoc doctor explain <id> # details for one suggestion
```

**For complete results,** give your terminal app **Full Disk Access** (System Settings › Privacy & Security › Full Disk Access), then restart the terminal. Without it, macdoc reports how much space it couldn't see (about 36 GB on the dev Mac), and lists the locations.

Scan results live in `~/Library/Application Support/macdoc/state.db` (SQLite, mode 0600). The last 10 scans are kept.

---

## What it is

macdoc is a **storage doctor for developer Macs, with a built-in explanation of why the Mac is slow.** It:

- **Diagnoses** the machine: hardware, APFS container usage, purgeable space, local Time Machine snapshots, swap and memory pressure.
- **Explains where storage went**, including the opaque "System Data" bucket, broken down by real causes: Xcode, Android, Node/React Native, Docker, Python, local AI models, Homebrew and stale projects.
- **Finds reclaimable space.** Every item has an honest size (measured, tool-reported, estimated or unknown), a risk level (SAFE / CAUTION / RISKY / ADVISE), its idle time, and what it costs to get back.
- **Plans cleanups as dry-runs first** *(Phase 5)*. Nothing is removed until you review a sealed plan and confirm it in your terminal.
- **Cleans safely** *(Phase 6)*. It uses each tool's own cleanup command, or moves items to a restorable quarantine, and keeps an audit log.
- **Connects to Claude** *(Phase 8)* through a local MCP server. Claude can diagnose and *propose* plans, and only you can apply them.

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
2. **Honest numbers.** Allocated bytes, not logical size. Hard links are counted once. Every number says where it came from, and unknown is never shown as 0.
3. **Safety is the product.** Read-only by default. Actions come only from a fixed recipe catalog with exact command allow-lists, and never from arbitrary paths. There's a dry-run before anything happens, terminal confirmation, a restorable quarantine, and an audit log.
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
| 3 | 46 recipes + classifier; one `macdoc doctor` overview with sections (`health`, `space`, `wins`, `explain`, `dev`, `scan`) | ✅ done (red-teamed, 12 findings fixed) · **awaiting user review** |
| 4 | Project awareness: stale `node_modules` by git activity, Pods, RN/Android build folders | next |
| 5 | Planner: sealed, expiring dry-run plans → `plan` | |
| 6 | Executor + quarantine + audit log → `apply` (the first phase that can delete) | |
| 7 | `slow`, `trend`, JSON schemas, polish → **v0.1 (MVP B)** | |
| 8–9 | MCP server for Claude (read + propose plans), handoff, hardening | |
| 10 | Optional daily read-only scans, TUI picker, slowness tips | |
| 11 | Open-source readiness | later |

Each completed phase has a checkpoint in [`docs/checkpoints/`](docs/checkpoints/), with real-Mac measurements, the tasks run, and the safety findings.

## Safety model, as implemented so far

- **Path safety** (`internal/safety`):
  - Canonicalization that rejects `..` and trailing `/`, resolves symlinked parents, normalizes Unicode and ignores case.
  - Protected paths: system roots, `~/Documents`/`Desktop`/…, iCloud, Mail, Keychains, `.ssh`, `.git`, `.env*`, SQLite files and their sidecars, Containers/`com.apple.*`, parents of any protected folder, and other users' homes.
  - **No cleanup target can be outside `$HOME`.**
  - 99.5% coverage, fuzz-tested.
- **Recipe lint** (`internal/recipes/load.go`), applied to every recipe at load time:
  - Native and dry-run commands must **exactly match an allow-list** of 18 and 3 commands.
  - Allow patterns must be `~`-relative and at least 2 deep, with no `..`, and can't reach protected folders.
  - Preconditions use a strict character set.
- **Classifier:**
  - Drops a finding if the tool is missing, the identifier is unsafe or reserved (`all`, …), it fails `CheckTarget`, it was used too recently, or its age is unknown when a minimum idle time applies.
  - Raises risk for anything changed in the last 7 days.
  - Merges overlapping and duplicate findings.
- **Detectors are read-only.** File reads go through a regular-file, non-blocking, size-capped reader. External commands go only through `platform.Runner`. Detectors never run package managers to discover things. (Querying pnpm/yarn once triggered Corepack downloads, so that's now forbidden.)
- **CI guards:** `importcheck` (MCP and the engine can't import the executor) and `forbidcheck` (no delete, rename, exec or shell calls outside the executor, aliased imports included). Both are verified against planted violations.

Full details: [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md).

---

## Architecture

```
macdoc (one Go binary)
  cmd/macdoc            entry point
  internal/cli          `macdoc doctor` overview + sections (health, space, wins, explain, dev, scan), version
  internal/engine       UI-agnostic core: Overview, Doctor, Scan, Opportunities (classifier/ranking), Storage
  internal/probe        hardware, memory, swap, processes, APFS, snapshots (read-only, via Runner)
  internal/scanner      parallel full crawl: allocated bytes, hard links once, bounded memory, background QoS
  internal/store        SQLite state (scans, nodes, unmeasured paths, history), pure Go
  internal/recipes      recipe schema, loader + safety lint, detectors, catalog/*.yaml (46 recipes)
  internal/safety       canonicalization + protected-path policy
  internal/sandbox      MACDOC_SANDBOX for executor tests (fails closed)
  internal/platform     read-only command Runner (+ fake), darwin cgo shim for purgeable space
  internal/model        shared types: Opportunity, Plan, Node, Envelope …
  internal/executor     (Phase 6) the ONLY package allowed to delete or move
  internal/mcp          (Phase 8) MCP server; may never import executor
  internal/testutil     fstree fixture builder for fake home directories
  tools/forbidcheck     AST guard against delete/exec/shell calls outside the executor
```

The CLI and (later) the MCP server are two front doors over the same engine. See [docs/BUILD_STRATEGY.md](docs/BUILD_STRATEGY.md).

## Development

```bash
make build        # ./bin/macdoc
make install      # build + copy to ~/.local/bin (BINDIR=… to change)
make test         # go test -race ./...
make lint         # golangci-lint (0 issues required)
make importcheck  # MCP/engine must not reach the executor
make forbidcheck  # no delete/exec/shell outside internal/executor
make check        # all of the above
make fuzz         # fuzz the path-safety layer
```

Rules for contributors (human or AI) are in [AGENTS.md](AGENTS.md), and Claude Code specifics are in [CLAUDE.md](CLAUDE.md).

### How it's being built
A **tech-lead Claude session** writes the contracts, and delegates bounded tasks to **headless worker Claude sessions** that run in visible tmux panes, each in its own git worktree. The tech lead verifies every task independently (tests, lint, guards, real-Mac runs, planted violations). **Red-team reviewers** attack the safety-critical code, and every finding becomes a test. Workers can only write inside their worktree, use a narrow command allowlist, and keep the Mac awake while running. Details are in [docs/ORCHESTRATION.md](docs/ORCHESTRATION.md). Every task brief is in [`docs/tasks/`](docs/tasks/), and every report in [`docs/tasks/reports/`](docs/tasks/reports/).

## Documents

| File | What's in it |
|---|---|
| [docs/DEVELOPMENT_PLAN.md](docs/DEVELOPMENT_PLAN.md) | Architecture, phases, tasks, acceptance criteria, testing, risks, carry-overs |
| [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md) | Risk classes, protected paths, the plan → confirm → apply → verify lifecycle, quarantine, audit, threat model |
| [docs/RECIPE_CATALOG.md](docs/RECIPE_CATALOG.md) | Recipe schema, catalog intent, allowed native commands, how to add a recipe |
| [docs/CLI_UX.md](docs/CLI_UX.md) | Commands, report mockups, tone rules, JSON contract |
| [docs/MCP_DESIGN.md](docs/MCP_DESIGN.md) | Phase 8–9: MCP tools, the Claude-to-terminal handoff, prompt-injection hardening |
| [docs/BUILD_STRATEGY.md](docs/BUILD_STRATEGY.md) | One engine with two front doors, and the safety-first build order |
| [docs/ORCHESTRATION.md](docs/ORCHESTRATION.md) | Tech lead + worker agents, permissions, verification, checkpoints |
| [docs/checkpoints/](docs/checkpoints/) | Per-phase results measured on the real Mac |
| [docs/tasks/](docs/tasks/), [docs/tasks/reports/](docs/tasks/reports/) | Task briefs and archived worker/reviewer reports |

## Decisions locked

| Topic | Decision |
|---|---|
| Scope | MVP B (diagnosis, dry-run planner, terminal-confirmed apply, audit log), then C (Claude via local MCP) as a pure addition |
| Default mode | Read-only. Read-only commands never delete anything, including expired quarantine |
| Removal | The owning tool's native command first (from an exact allow-list). Otherwise a macdoc-managed quarantine that auto-expires |
| Targets | Never outside `$HOME` by path. Anything outside home is only ever touched through native tool commands |
| Scanning | Full crawl of the Data volume, background priority, streaming aggregation, bounded memory |
| Language | Go. Single binary, with an optional cgo shim for purgeable space |
| Audience | Personal first, open-source later. Recipes are data-driven |
| Claude boundary | Claude can diagnose and propose plans. Only a human at a real terminal can apply them. The MCP process contains no deletion code |
