# Checkpoint: Phase 6 (Executor + quarantine + audit → `apply`, `quarantine`, `log`) ✅

**Date:** 2026-10-28 · **main:** see `git log` · **Status:** complete. This is the first phase that can change files. No real apply, purge or native cleanup was run on the development Mac: every executor test ran under `MACDOC_SANDBOX`.

## Built
- **`internal/executor`**, the only package that moves, deletes or execs:
  - `Apply` verifies the plan with a fresh policy, then locks it (`MarkApplied`, pending → applied), so a plan runs at most once. It writes `apply_started`, and if that write fails, nothing changes.
  - `Apply` performs the actions through `planner.Perform`, the same translation the dry run uses. Each item is re-checked: approval, recipe and method, preconditions, then the move or command checks below. A failed check means the item is skipped and audited, never "fixed up".
  - Quarantine move:
    - record a pending entry, then write the manifest;
    - open the parent and payload folders with `O_NOFOLLOW`, check the inode with `fstatat`, then `renameatx_np(RENAME_EXCL)`;
    - afterwards check what arrived and that the parent didn't change, and move it back if either check fails;
    - finally mark the manifest and record done.
  - Before the move: a content check (`.git`, bare repos, `.env*`, except inside `node_modules` / `site-packages`; at most 500,000 entries), a same-volume check, and a re-size of SAFE items whose mtime changed. Other changed items are skipped.
  - Native commands use a fixed argv, no shell, empty stdin, a 15-minute timeout and an output tail. A `{id}` must still be in the tool's own listing, checked by re-running the detector. Native commands are refused in sandbox mode with the real runner.
  - `Restore` never overwrites, and re-checks the original path and the payload inode.
  - `Purge` acts only on entries with macdoc's store record and a matching manifest, in a real folder in the root. Its `os.RemoveAll` is the package's only recursive delete.
- **`internal/audit`**: append-only JSONL, `flock` plus `fsync` per record, torn lines tolerated.
- **`internal/quarantine`** (read-only): manifests and entry states (held, interrupted, not-moved, missing, damaged), plus listing that cross-checks the store.
- **`internal/precond`** (read-only): app not running, emulator, Simulator, Gradle daemons, Metro, Docker reachable, ollama model not loaded, git clean. Checks that can't run count as failed.
- **Store v3:** the `quarantine` table; `MarkApplied`.
- **Engine:** `FreeSpace`, `Quarantine`, `AuditLog`; `CreatePlan` writes `plan_created`; the overview reports what's held and expired.
- **CLI:**
  - `macdoc apply <P-id>` checks for a real terminal with an ioctl test (no `--yes`, no `--json`). It asks `[y/N]` per item for RISKY items and for every item of a Claude-proposed plan, then for the typed code. It shows progress per item and a summary with the measured free-space change.
  - `macdoc quarantine [list|restore|purge]`; purge asks for the typed word `purge`.
  - `macdoc log`.
  - The plan footer now says `To apply it: macdoc apply P-…`.

## Done-when
| Criterion | Test |
|---|---|
| All executor tests run under `MACDOC_SANDBOX` | `TestMain` (`internal/executor`) creates one if it's unset and refuses an invalid one. CLI apply tests set their own. `TestSandboxBoundary`: an empty variable refuses the whole apply, and a target outside the sandbox is skipped |
| A symlink swapped in between plan and apply is detected and skipped | `TestSwapsBetweenPlanAndApply`: target → symlink, parent → symlink (the whole plan is refused by Verify), replaced folder, deleted. `TestSwapDuringMove`: a swap after the checks, just before the rename, is caught by the identity check on the opened parent |
| Killing the process mid-apply leaves a consistent audit log and a recoverable quarantine | `TestCrashMidApply`: a child process exits at `pending`, `moved` and `done`. In every case the audit log parses cleanly, the plan can't run again, and the entry is restorable (and restore brings it back) or untouched (the original was never moved; purge tidies the empty entry) |
| Nothing is removed except native output or a purge of a manifest-backed entry | `TestMutatingCallSites` pins every delete, rename and exec call to a named function. `TestPurge` and `TestDamagedEntriesUntouched` cover folders without a record, edited manifests and payloads replaced by symlinks |

Also tested:
- plans refused before any change (edited, expired, unknown) leave the plan pending with no audit records
- one apply wins out of four concurrent ones
- unapproved items are skipped
- content checks, including vendored and look-alike files
- changed SAFE items are re-sized; changed CAUTION items are skipped
- preconditions failing, or unable to be checked
- native: done, failed, tool missing, identifier no longer listed, model loaded
- items are independent
- restore never overwrites
- CLI: no terminal, wrong codes, the confirmed apply, RISKY y/N, Claude-origin per-item prompts, declining everything, edited plans, list/restore/purge flows, log rendering, the overview's quarantine line

## Real Mac (read-only)
- `macdoc plan --safe` → P-85BV; `macdoc log` shows its `plan_created` record.
- `echo … | macdoc apply P-AAAA` → refused ("needs you at a Terminal"). `macdoc apply ../etc` → refused (not a plan ID). `echo purge | macdoc quarantine purge --expired` → refused.
- `macdoc quarantine list` → "The quarantine is empty."
- I didn't drive `apply` through a pseudo-terminal on this Mac: the session's permission check correctly blocked that as a TTY-gate bypass. Confirming a real apply is for the user to do.

## Not done (by design or later)
- No `--yes`, even for SAFE-only plans. That's stricter than SAFETY_MODEL §4.2 allowed, and the doc is updated.
- When a parent folder turns into a symlink into a protected place, the whole plan is refused (by Verify) rather than just that item. It's safe, but coarser.
- Old plan files and `canceled`/`restored`/`purged` store rows aren't pruned yet (Phase 7 polish).
- No config file yet for the quarantine TTL (7 days) or the native timeout (15 minutes).

## Try it yourself
```
macdoc plan --safe          # or pick IDs: macdoc plan O-…
macdoc apply P-XXXX         # in Terminal; type the code when asked
macdoc quarantine           # what's held; restore with macdoc quarantine restore Q-…
macdoc log
```
