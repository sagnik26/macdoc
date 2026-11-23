# 0014: `reclaim` may clean system locations, sweep `.DS_Store` and run with `--yes`, under named limits

**Status:** Accepted (owner decision, 2026-11-18). **Implemented: all five clauses.** It supersedes ADR 0009, `AGENTS.md` rules 3, 4 and 6, and `docs/SAFETY_MODEL.md` §3 only for items `macdoc reclaim` offers; every other command keeps the old rules. Each numbered clause stands alone, so the owner can accept, change or drop any one of them.

## Context

`mo clean` (Mole) also removes system caches and logs, orphaned LaunchDaemons and helpers, interrupted Time Machine backups and `.DS_Store` files, and runs without a prompt with `--yes`. Three rules stop `reclaim` from doing the same:

| Wanted | Rule in the way |
|---|---|
| `/Library/Caches`, `/private/var/log`, LaunchDaemons, Time Machine | Rule 6 (protected paths) and ADR 0009 (root only for three exact argvs, no path) |
| `.DS_Store` anywhere in home | Rule 6 (the file sits inside Documents, Desktop, Pictures …) |
| `reclaim --tier safe --yes` | Rule 3 / ADR 0001 (no non-interactive bypass) |

The owner decided to keep those rules everywhere else and loosen them for `reclaim` only.

## Decision

### 1. System locations (root), by name

A recipe may target only these locations with root:

- `/Library/Caches/<item>` and `/Library/Logs/<item>`, one level down
- `/private/var/log/<item>`, one level down
- `<TimeMachineDestination>/Backups.backupdb/*/*.inProgress`
- `/Library/LaunchDaemons/<file>.plist` and `/Library/PrivilegedHelperTools/<file>`, one file each, only when the owning app is confirmed gone (the installed-app check `reclaim` already uses)

Everything else stays protected: `/System`, `/usr`, `/bin`, `/sbin`, `/cores`, `/Applications` (except ADR 0011) and any path not listed. The list is code (like `PrivilegedArgvAllowed`), not configuration; changing it needs a new ADR.

### 2. How root runs

A new method, `system_clean`, runs only from `internal/executor/privileged.go` (the one file that may name `sudo`), with a fixed program and arguments. Immediately before acting it re-validates the target: inside the list, no symlink, no `..`, owned by root, a regular file or a folder, same volume. Time Machine uses `tmutil delete` with the validated path. `sudo -k` runs afterwards. Root-owned data cannot be quarantined, so these removals are permanent; the dry run says "cannot be restored".

### 3. Tiers shown before cleaning

| Item | Tier | Shown as |
|---|---|---|
| System caches, system logs | CAUTION | needs your password, can't be restored |
| `.DS_Store` sweep | CAUTION | can't be restored (Finder recreates them) |
| Interrupted Time Machine backup | RISKY | needs your password, can't be restored |
| Orphaned LaunchDaemon or helper | RISKY | needs your password, can't be restored |

None is SAFE, because every one is permanent. Root items never ride the single Enter of the CAUTION box: each asks its own y/N (ADR 0009); RISKY keeps `apply`'s plan, per-item y/N and typed code.

### 4. `.DS_Store` sweeps

One exact file name, `.DS_Store`, as a regular file (never a symlink or folder), may be removed anywhere under home, including protected folders. Still never touched: `Library/Mail`, `Library/Mobile Documents`, `Library/CloudStorage`, `Library/Application Support/MobileSync`, macdoc's own folder, any `.git` folder, `*.photoslibrary`, `*.musiclibrary`, and anything on another volume. A `ds_store` detector emits one item per folder directly in home (for example "Finder view files in ~/Projects", with the count as its note), and the `sweep` method removes only base names exactly `.DS_Store`, re-checking each file right before removal and recording the count in the audit.

As built: the recipe is `system.ds_store` (`catalog/dsstore.yaml`), CAUTION, method `sweep`, with no allow patterns or command; the loader accepts that method only for the `ds_store` detector at CAUTION. A sweep may start only at a folder directly in home that is not hidden and not `Library` (`safety.Policy.CheckSweepRoot`), and it never enters `.git`, `node_modules`, `.Trash`, `*.photoslibrary`, `*.musiclibrary`, a folder you protected in settings, or another volume. Files are removed with `unlink`, so a folder named `.DS_Store` can't be removed, and a symlink is never followed. The sweep is deleted at once with no quarantine copy, and the dry run says so. It is never in a plan Claude proposed (the planner refuses it, `planner.Verify` rejects it, the executor skips it). It rides the CAUTION Enter in `reclaim` like any other non-root CAUTION item. Like other small items it is listed by default once its total passes 1 MB, and under `--small` before that.

### 5. `reclaim --tier safe|caution --yes`

`--yes` needs an explicit `--tier`; a bare `--yes` does nothing. It cleans only SAFE and CAUTION, non-privileged, quarantine-method items. RISKY, root, `.DS_Store`, advice items and plans Claude proposed never run with `--yes`. It is refused while another macdoc operation runs, works only against the sandbox when the sandbox is on, and `macdoc apply` gets no `--yes`. Audit records carry `non_interactive: true`. This amends ADR 0001 for `reclaim` only.

**Dashboard `/reclaim` (2026-11-24, the owner's decision).** In the dashboard, `/reclaim` has no checkbox list and no confirmation box. When its own fresh scan finishes it marks every SAFE and CAUTION suggestion (never RISKY, advice or an installed app), seals a plan from those IDs and runs it through the executor at once, with progress by category. `esc` cancels the run's context, so the remaining items are skipped as interrupted. The same limits as `--yes` hold: SAFE and CAUTION only, quarantine-restorable, every item re-validated before it is acted on, audited. The person's approval is typing `/reclaim` (or choosing it) in a real terminal; no agent or script can start it, because it exists only inside the interactive dashboard and the executor still refuses without a real TTY. RISKY items are never part of that run; when it ends the screen counts them and offers `d`, which opens the ordinary removal box with its typed-ID confirmation for every RISKY item.

## What this gives up

- ADR 0001 existed so that only a person could confirm a deletion. With `--yes`, anything that can run a command as the user (a script, cron, an AI agent with a shell) can clean SAFE and CAUTION items. Quarantine keeps those restorable until purge, only the lowest two tiers are reachable, the MCP server still has no apply tool and cannot pass `--yes`, and `macdoc schedule` still only scans.
- Clauses 1 to 4 add a second place that deletes outside quarantine. They are held to the same guards as ADR 0009: terminal only, own y/N, never from a plan Claude proposed, the MCP server gains no tool and cannot plan them (for the sweep, `create_cleanup_plan` refuses it).
- `AGENTS.md` "Never" list gains: do not run `macdoc reclaim … --yes` on this machine, and never run a system-clean item.

## Tests

Clause 4, in place: `internal/safety/tests.TestCheckSweepRoot`, `internal/safety/tests.TestSweepSkips`, `internal/recipes/tests.TestDetectDSStore`, `internal/recipes/tests.TestSweepLoaderRefusals`, `internal/planner/tests.TestBuildSweep`, `internal/executor/tests.TestSweepRemovesOnlyDSStore`, `internal/executor/tests.TestSweepRefusedRoots`, `internal/executor/tests.TestSweepFolderBecameLink`, `internal/executor/tests.TestSweepNeverInClaudePlan`. The list below is the whole decision.

Target outside the list refused; look-alikes (`/Library/Caches-old`, `/private/var/logs`, `.DS_Store.bak`, `._.DS_Store`) refused; symlink into `/System` refused; not root-owned refused; Claude-origin plan refused; no terminal refused; sandbox refused; per-item y/N required; audit carries `privileged: true`; bare `--yes` refused; `--yes` with RISKY, root, `.DS_Store`, advice or Claude-origin refused; `apply` has no `--yes`; `TestToolList` unchanged.

## As built: clauses 1 to 3

- Method `system` (`model.MethodSystem`), recipes in `catalog/systemclean.yaml`: `system.library_caches`, `system.library_logs`, `system.var_logs` (CAUTION, idle 30 days), `system.launch_daemons`, `system.helper_tools`, `system.tm_incomplete` (RISKY). The loader pins each kind (`params.kind`) to its detector and risk and refuses allow patterns, commands and the privileged flag.
- The location list is `safety.Policy.CheckSystemTarget`: one item in the named layouts, root-owned, not a symlink, not `com.apple.*` (caches, daemons, helpers), not inside a folder you protected. The detectors (`internal/recipes/elevated.go`) only read; the engine, planner, `Verify` and the executor each call the check again.
- The executor (`internal/executor/system.go`) re-checks the device and inode, the rules, the idle time and that the recipe's detector still finds the item (an orphan is still an orphan), then builds the command itself (`recipes.SystemArgv`: `rm -rf -- <path>`, `rm -f -- <path>` for one file, `tmutil delete <path>`). `privileged.go` accepts only that shape (`recipes.SystemArgvOK` with `safety.SystemShapeOK`) and still needs a terminal, no sandbox and the password at the system prompt; `sudo -k` follows. Plan items are marked privileged, so each needs its own y/N, and `reclaim` sends a chosen tier that contains one through apply's plan, y/N per item and typed code.
- The old advice-only `tm.incomplete_backups` stays as a count; `system.tm_incomplete` lists the folders themselves.
- The planner refuses a `system` item in a Claude plan, so an agent can see these in `list_opportunities` but never plan them.
- Tests are named in the threat model (T23).
