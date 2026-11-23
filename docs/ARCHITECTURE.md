# macdoc design notes

Moved out of the README so it can follow a usage-first layout. Nothing here is a promise about behaviour: the safety rules are in [SAFETY_MODEL.md](SAFETY_MODEL.md) and the decision records in [adr/](adr/README.md).

## What it is

macdoc is a **storage doctor for developer Macs, with a built-in explanation of why the Mac is slow.** It:

- **Diagnoses** the machine: hardware, APFS container usage, purgeable space, local Time Machine snapshots, swap and memory pressure.
- **Explains where storage went**, including the opaque "System Data" bucket, broken down by real causes: Xcode, Android, Node/React Native, Docker, Python, local AI models, Homebrew and stale projects.
- **Finds reclaimable space.** Every item has an honest size (measured, tool-reported, estimated or unknown), a risk level (SAFE / CAUTION / RISKY / ADVISE), its idle time, and what it costs to get back.
- **Plans cleanups as dry-runs first.** Nothing is removed until you review a sealed plan and confirm it (typed code in Terminal, or a second Enter in the dashboard).
- **Cleans safely.** It uses each tool's own cleanup command, or moves a folder through macdoc's quarantine and deletes it there, so the space is freed at once and a crash never leaves a half-deleted folder. Every step is in an audit log.
- **Connects to Claude** through a local MCP server (`macdoc mcp`). Claude can diagnose and *propose* plans, and only you can apply them.

It is **not** a "Mac cleaner" in the usual sense. There's no background daemon, no automatic deletion, no "RAM boosting", no killing processes, and a health score only from fixed rules it shows (`vitals`).

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

## Safety model, as implemented so far

- **Path safety** (`internal/safety`):
  - Canonicalization that rejects `..` and trailing `/`, resolves symlinked parents, normalizes Unicode and ignores case.
  - Protected paths: system roots, `~/Documents`/`Desktop`/…, iCloud, Mail, Keychains, `.ssh`, `.git`, `.env*`, SQLite files and their sidecars, Containers/`com.apple.*`, parents of any protected folder, and other users' homes.
  - **No cleanup target can be outside `$HOME`, except one exact shape: an app bundle directly in `/Applications` (ADR 0011).**
  - 99.5% coverage, fuzz-tested.
- **Your protected folders** (`macdoc config protect <folder>`, `internal/config`): added on top of the built-in list, never replacing it. Suggestions, planning and the executor's last check all honor them, and an unreadable settings file stops those with an error instead of silently dropping a folder.
- **Recipe lint** (`internal/recipes/load.go`), applied to every recipe at load time:
  - Native and dry-run commands must **exactly match an allow-list** (one for cleanup commands, one for dry runs, and the fixed shapes the `system` method may run as administrator, ADR 0014).
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
  internal/cli          commands and rendering: the dashboard, the commands (`reclaim`, `retire`, `explore`, `vitals`, `scan`),
                        quarantine, log, config, version
  internal/engine       UI-agnostic core: Overview, Doctor, Slow, Trend, Scan, Opportunities (classifier/
                        ranking), Storage, Projects, plans, audit and quarantine views
  internal/probe        hardware, memory, swap, processes, APFS, snapshots (read-only, via Runner)
  internal/scanner      parallel full crawl: allocated bytes, hard links once, bounded memory, background QoS
  internal/store        SQLite state (scans, nodes, unmeasured paths, plans, quarantine, vitals), pure Go
  internal/recipes      recipe schema, loader + safety lint, detectors, catalog/*.yaml (60 recipes)
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

### How it's being built
A **tech-lead Claude session** writes the contracts, and delegates bounded tasks to **headless worker Claude sessions** that run in visible tmux panes, each in its own git worktree. The tech lead verifies every task independently (tests, lint, guards, real-Mac runs, planted violations). **Red-team reviewers** attack the safety-critical code, and every finding becomes a test. Workers can only write inside their worktree, use a narrow command allowlist, and keep the Mac awake while running. Details are in [docs/ORCHESTRATION.md](docs/ORCHESTRATION.md).

## Decisions locked

| Topic | Decision |
|---|---|
| Scope | MVP B (diagnosis, dry-run planner, terminal-confirmed apply, audit log), then C (Claude via local MCP) as a pure addition |
| Default mode | Read-only. Read-only commands never delete anything, including expired quarantine |
| Removal | The owning tool's native command first (from an exact allow-list). Otherwise a move into macdoc's quarantine, deleted right away so the space is freed (2026-10-28; originally held for 7 days) |
| Targets | Never outside `$HOME` by path, with one exception: an app bundle directly in `/Applications` (ADR 0011). Anything else outside home is only touched through native tool commands, and a few maintenance commands run with administrator rights only through `internal/executor/privileged.go` (ADR 0009) |
| Scanning | Full crawl of the Data volume, background priority, streaming aggregation, bounded memory |
| Language | Go. Single binary, with an optional cgo shim for purgeable space |
| Audience | Personal first, open-source later. Recipes are data-driven |
| Claude boundary | Claude can diagnose and propose plans. Only a human at a real terminal can apply them. The MCP code can't reach the deletion code (CI import guard), and no MCP tool acts on the Mac |
