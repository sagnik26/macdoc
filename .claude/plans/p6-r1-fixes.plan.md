# Plan: Fix the P6-R1 executor red-team findings (F1–F8)

**Source report**: `docs/tasks/reports/P6-R1.md` (merged to `main` at `4f55d56`)
**Selected milestone**: P6-R1 resolution (Phase 6 red-team follow-up)
**Complexity**: Medium (8 small, focused executor changes, 1 small `recipes` addition, docs)
**Status**: awaiting approval, no code written yet
**Date**: 2026-11-02

## Summary

The P6-R1 red-team found no way to delete user data outside the recipe allowlist. It did find 8 weaker spots in `internal/executor`, and each one has a deliberately failing `TestRedteamBroken*` test. This plan fixes all 8. Every fix makes the executor **stricter**: it refuses or skips more, never less. Once all 8 are fixed, the tests are renamed to `TestRedteamFixed*`, the report gets a Resolution section, and the safety docs and checkpoints are updated. This mirrors how P9-R1 was closed (`681a945`).

### Baseline (verified 2026-11-02 after the merge)

`go test -race ./internal/executor/... ./internal/planner/... ./internal/quarantine/...` fails only on:

| ID | Sev | Failing test (`internal/executor/redteam_test.go`) | Line |
|---|---|---|---|
| F1 | Medium | `TestRedteamBrokenNestedMountDeleted` | 50 |
| F2 | Medium | `TestRedteamBrokenContentCheckIgnoresCase` | 98 |
| F3 | Low | `TestRedteamBrokenBareRepoSymlinkHead` | 121 |
| F4 | Medium | `TestRedteamBrokenSymlinkedQuarantineRoot` | 141 |
| F5 | Low | `TestRedteamBrokenAncestorSwapDuringMove` | 178 |
| F6 | Low | `TestRedteamBrokenSandboxStateDirOutside` | 216 |
| F7 | Low | `TestRedteamBrokenPurgeWithoutAudit` | 251 |
| F8 | Low | `TestRedteamBrokenRelistNeedsScan` | 281 |

Everything else passes, including the 15 `TestRedteamHeld*` tests.

## Safety invariants this work must keep (AGENTS.md)

- All deletes, moves and mutating calls stay in `internal/executor` (invariant 2). The F8 listers in `recipes` are **read-only**.
- No new import of `internal/executor` from `mcp`, `engine` or `planner` (invariant 3).
- No shell, no string-built commands (invariant 8). Re-listing uses the existing `platform.Runner` and fixed argv.
- Re-validate, then skip and audit on any mismatch. Never "fix up" a target (invariant 7).
- Tests run only under `MACDOC_SANDBOX` with `t.TempDir()` fixtures (`executor_test.go:28` TestMain). No real cleanup on the dev Mac.

## Patterns to mirror

| Category | Source | Pattern |
|---|---|---|
| Skipping an item | `internal/executor/perform.go:37` `skipf` | Return `skipf("neutral reason …")`; `do()` audits it as `item_skipped` |
| Folder open | `internal/executor/move.go:290` `openDir` | `O_RDONLY\|O_DIRECTORY\|O_NOFOLLOW\|O_CLOEXEC` via `golang.org/x/sys/unix` |
| Identity check | `internal/executor/move.go:296` `sameEntry` / `:311` `sameDir` | Compare `Dev`+`Ino` from `Fstat`/`Fstatat(AT_SYMLINK_NOFOLLOW)` |
| Name comparison | `internal/safety/canonical.go:80` `fold` | NFC → Unicode case fold → NFC, the same comparison as protected paths |
| Sandbox gate | `internal/sandbox/sandbox.go:46` `Check` | Call **before** any write |
| Audit | `internal/audit/audit.go:78` `Append`, `:60` `Path` | JSONL append; schema frozen by `TestSchemasFrozen` |
| Detector registry | `internal/recipes/registry.go:22` `Register` / `:32` `Lookup` | Name → func, registered in `init()` |
| Red-team test naming | `internal/mcp/redteam_test.go` | `Broken` → `Fixed` once fixed; `Held` tests untouched |
| Report close-out | `docs/tasks/reports/P9-R1.md` `## Resolution (tech lead, <date>)` | One line per finding: fix + test |
| User-facing text | `docs/CLI_UX.md` §3 | Neutral wording, no scare language |

## Files to change

| File | Action | Why |
|---|---|---|
| `internal/safety/canonical.go` | UPDATE | Export `FoldName(s string) string` (wraps `fold`) for F2 |
| `internal/executor/move.go` | UPDATE | F1 (dev check in `checkContents`), F2/F3 (folded names, symlinked HEAD), F4 (`ensureRoot`), F5 (`openDirStrict`, `sameDir`) |
| `internal/executor/executor.go` | UPDATE | F4/F6 (`tightenStateDir` checks, sandbox gate on the state dir in `Apply`) |
| `internal/executor/manage.go` | UPDATE | F1 (`removeEntry` device check), F5 (Restore uses the strict open), F6 (sandbox gate in `Restore`/`Purge`), F7 (audit probe in `purgeOne`/`Restore`) |
| `internal/executor/perform.go` | UPDATE | F8: `stillListed` prefers a registered lister |
| `internal/audit/audit.go` | UPDATE | F7: `Probe(path string) error` (open for append, close) |
| `internal/recipes/registry.go` | UPDATE | F8: `RegisterLister` / `LookupLister` |
| `internal/recipes/detect_androidsdk.go` | UPDATE | F8: lister for `android_avds` (from `listAVDs`, `:98`) |
| `internal/recipes/detect_xcode.go` | UPDATE | F8: lister for `simctl_idle_devices` (from `simDevices`, `:217`) |
| `internal/recipes/*_test.go` | UPDATE | F8: lister positive / look-alike / tool-missing tests |
| `internal/executor/redteam_test.go` | UPDATE | Rename 8 `Broken*` → `Fixed*`; flip assertions where needed |
| `internal/executor/*_test.go` | UPDATE | Extra unit tests listed per task |
| `docs/tasks/reports/P6-R1.md` | UPDATE | `## Resolution` section |
| `docs/SAFETY_MODEL.md` | UPDATE | §4.3, §4.4, §4.6, §4.7 (see Task 8) |
| `docs/checkpoints/PHASE-6.md` | UPDATE | Red-team section + deferred notes under Open |
| `docs/checkpoints/PHASE-9.md` | UPDATE | Remove "No report of the P6-R1 executor red-team exists" from Open |

## Tasks

### Task 0: Merge the red-team branch ✅ done

- Fast-forwarded `main` to `4f55d56`, removed the worktree and the branch. Baseline above confirmed.

### Task 1: F2, case-insensitive content check (Medium)

- **Problem**: `checkContents` (`move.go:109`) compares `name == ".git"` and `HasPrefix(name, ".env")` case-sensitively. APFS is case-insensitive, so `.GIT`, `.Git`, `.ENV` and `.Env.local` slip through and get moved and deleted.
- **Action**:
  1. Add `safety.FoldName(s string) string { return fold(s) }`.
  2. In `checkContents`, compute `f := safety.FoldName(name)` once per entry. Use `f == ".git"` and `strings.HasPrefix(f, ".env")`.
  3. For the bare-repo case, compare `FoldName(name) == "head"`.
  4. **Keep `vendorDirs` case-sensitive.** Folding it would widen the exemption, the wrong direction.
- **Validate**: `TestRedteamFixedContentCheckIgnoresCase` (renamed), plus a unit test that `NODE_MODULES/x/.git` **is** refused.

### Task 2: F3, bare repo with a symlinked HEAD (Low)

- **Problem**: the bare-repo case requires `d.Type().IsRegular()`. Git also reads a symlinked `HEAD`.
- **Action**: accept `HEAD` when `d.Type().IsRegular() || d.Type()&fs.ModeSymlink != 0`, still requiring sibling `objects/` and `refs/` folders (`isDir` uses `Lstat`, so symlinked `objects`/`refs` stay unaccepted, which is the current behaviour).
- **Validate**: `TestRedteamFixedBareRepoSymlinkHead`. Also check that a lone `HEAD` symlink with no `objects/`/`refs/` is still allowed (no false positive).

### Task 3: F1, another volume mounted inside a target (Medium)

- **Problem**: `checkContents` walks into a nested mount, and the move carries the live mount into quarantine. Only `os.RemoveAll` hitting `EBUSY` saved the files on the mounted volume.
- **Action**:
  1. `checkContents`: `Lstat` the root to get `rootDev`. For each **directory** entry (mount points are directories), get `d.Info()` and compare `Sys().(*syscall.Stat_t).Dev`. If it differs, `skipf("another disk is mounted inside it (%s)", rel)`. Check before the vendored and `SkipDir` shortcuts, so a mount inside `node_modules` is also refused.
  2. `removeEntry` (`manage.go:225`), as defence in depth: before `os.RemoveAll`, walk the entry and refuse with `"another disk is mounted inside it; it isn't touched"` if any directory's dev differs from the entry's. The fallback chmod walk must not cross devices either.
- **Validate**: `TestRedteamFixedNestedMountDeleted`. It uses a real 2 MB `hdiutil` image in `t.TempDir()`; skip if `hdiutil` is missing. Add a unit test for `removeEntry` that fakes the dev mismatch with an injectable stat function, or rely on the hdiutil test if injection would add complexity.

### Task 4: F4 + F6, symlinked quarantine root or state dir, and a sandbox escape via the state dir (Medium + Low)

- **Problem (F4)**: `ensureRoot` (`move.go:177`) runs `MkdirAll`, `Chmod(0700)` and marker creation **through** a symlink, and calls `sandbox.Check` only afterwards. `tightenStateDir` (`executor.go:288`) has the same pattern.
- **Problem (F6)**: with `MACDOC_SANDBOX` set but the store outside it, apply still chmods the state dir, creates `quarantine/` and writes the audit log outside the sandbox.
- **Action**:
  1. New helper in `executor`: `checkPrivateDir(dir string) error`. It requires the following, and refuses otherwise:
     - `Lstat(dir)` is a real directory (not a symlink),
     - `st_uid == os.Getuid()`,
     - `safety.Canonicalize(dir) == dir`.
  2. `tightenStateDir`: `sandbox.Check(dir)`, then `MkdirAll`, then `checkPrivateDir`, then `Chmod`.
  3. `ensureRoot`: `sandbox.Check(root)` **first**, then `Mkdir`, then `checkPrivateDir(root)`, then `Chmod`, then the marker (`O_EXCL|O_NOFOLLOW`), then `tmutil`.
  4. `Apply`, `Restore`, `Purge`: right after `sandbox.Root()`, if the sandbox is active, `sandbox.Check(cfg.stateDir())` must pass before **anything** is written, the audit log included. Return an error ("nothing was changed").
- **Validate**: `TestRedteamFixedSymlinkedQuarantineRoot` (`~/Public` keeps its mode, no marker, no `Q-` entry, the target is untouched). `TestRedteamFixedSandboxStateDirOutside` (the outside dir keeps 0755, no `quarantine/`, no audit file). Add a unit test: a state dir owned by another uid is refused, if it can be faked. Otherwise document it as untested.

### Task 5: F5, ancestor swap during the move (Low)

- **Problem**: `openDir(parent)` follows symlinked **ancestors**. `sameDir` re-`Lstat`s the same path through the same swapped ancestor, so it always matches. SAFETY_MODEL §4.4 claims it checks "the canonical parent path".
- **Action**:
  1. New `openDirStrict(path string) (int, error)`: open `/`, then `openat(fd, component, O_RDONLY|O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC)` for each component of the (already canonical) path, closing the intermediate fds. Any symlinked component makes it fail with `ELOOP`/`ENOTDIR`, and the item is skipped as "its folder changed".
  2. `moveIn` (`move.go:209`): `pfd, err := openDirStrict(parent)`.
  3. `sameDir(dirfd, path)`: open `path` again with `openDirStrict`, `Fstat` both and compare `Dev`/`Ino`. Run it before **and** after the rename. A mismatch after the rename means `moveBack`.
  4. `Restore` (`manage.go:35`): use `openDirStrict` for the original's parent, and the strict `sameDir` after the rename.
  5. **Not doing** (decision below): refusing regular files with `st_nlink > 1`.
- **Validate**: `TestRedteamFixedAncestorSwapDuringMove` (the `~/Documents` link survives, the item is skipped). Existing `TestSwapsBetweenPlanAndApply` and `TestSwapDuringMove` still pass. New test: a fixture home reached via a symlink (e.g. under `/var/folders` → `/private/var/folders`) still works, because the target path is canonical.

### Task 6: F7, purge or restore without an audit trail (Low)

- **Problem**: `purgeAudited` (`manage.go:129`) and `Restore` audit only **after** acting. If `audit.jsonl` isn't writable, an entry is permanently deleted with no record.
- **Action**:
  1. `audit.Probe(path string) error`: `os.OpenFile(path, O_WRONLY|O_APPEND|O_CREATE, 0o600)` then `Close`.
  2. `purgeOne`: before `removeEntry`, `Probe`. On error the outcome is skipped: "the audit log can't be written, so nothing was changed".
  3. `Restore`: `Probe` before the rename. On error, return an error and change nothing.
  4. **No new audit event types.** The `log --json` schema stays frozen.
- **Validate**: `TestRedteamFixedPurgeWithoutAudit` (the entry is still present, the outcome is skipped). New: `TestRestoreWithoutAudit` (nothing moved). `go test ./internal/cli -run TestSchemasFrozen` passes unchanged.

### Task 7: F8, native re-listing needs scan nodes (Low, fails closed)

- **Problem**: `stillListed` (`perform.go:178`) re-runs the detector with `Env.Nodes == nil`. `detectAndroidAVDs` (`detect_androidsdk.go:233`) and `detectSimctlIdle` return nothing without nodes, so `avdmanager delete avd -n X` and `xcrun simctl delete {id}` can never run.
- **Action**:
  1. `recipes/registry.go`: `type Lister func(ctx context.Context, env Env, r Recipe) ([]string, error)`, plus `RegisterLister(detector string, l Lister)` and `LookupLister(detector string) (Lister, bool)`.
  2. `android_avds` lister: `ToolInstalled("avdmanager")`, then `listAVDs(env)`, keeping `a.ok && validNativeArg(a.name)`. No scan, no staleness.
  3. `simctl_idle_devices` lister: `simDevices(ctx, env)` UDIDs, filtered by `nativeArgRE`.
  4. `stillListed`: if `LookupLister(r.Detector)` exists, use it (exact string match on `arg`). Otherwise fall back to the detector, as today.
  5. Listers are read-only and use the `Runner` (golden-file fakes in tests).
- **Validate**: `TestRedteamFixedRelistNeedsScan` (`Pixel_5` is deleted via the fake exec). Recipe tests for each lister: positive; look-alike negative (`Pixel_5x`, a UDID prefix, `all`); tool missing means an empty list, not an error. `TestRedteamHeldNativeArgInjection` still passes.

### Task 8: Docs and close-out

- `internal/executor/redteam_test.go`: rename the 8 tests `Broken` → `Fixed` and update the header comment.
- `docs/tasks/reports/P6-R1.md`: `## Resolution (tech lead, 2026-11-02)`, one line per F1–F8 (fix + test name). Note the decision on `nlink`.
- `docs/SAFETY_MODEL.md`:
  - §4.3: names are matched ignoring case (same folding as protected paths); a target with another disk mounted anywhere inside is skipped; a symlinked bare-repo `HEAD` counts as a repo.
  - §4.4: the parent folder is opened one component at a time without following symlinks, before and after the move. The quarantine root and state dir must be real folders owned by the user, checked before any write. In sandbox mode the state dir must be inside the sandbox.
  - §4.6: purge refuses another-volume content inside an entry.
  - §4.7: purge and restore need a writable audit log before acting.
- `docs/checkpoints/PHASE-6.md`: add a "Red-team (P6-R1, 2026-11-02)" section. Under Open, list the deferred notes (below).
- `docs/checkpoints/PHASE-9.md`: remove the P6-R1 item from Open.
- `AGENTS.md`: no change expected (status wording still accurate). Check anyway.

## Order of work

`T1 → T2 → T3 → T4 → T5 → T6 → T7 → T8`

T1 and T2 touch the same `switch` in `checkContents`, and T3 touches the same function. That's why they come first and together. T4 before T5, because both edit `move.go` folder handling. T7 is independent and can be done at any point.

Run the executor tests after each task. Only the remaining `Broken` tests may fail.

## Validation

```bash
# after each task
go test -race ./internal/executor/... ./internal/planner/... ./internal/quarantine/... ./internal/recipes/... ./internal/audit/...

# before finishing
make check            # test (race) + lint + importcheck + forbidcheck
go test ./internal/cli -run TestSchemasFrozen   # must pass with no -update

# mutation check: undo each fix locally, confirm its Fixed test fails, restore
```

## Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| `openDirStrict` refuses legitimate setups (home behind a symlink, `/var` → `/private/var`, firmlinks) | Medium | Targets are canonicalized at plan and verify time, so components shouldn't be symlinks. Add a fixture test under `/var/folders`. If a firmlink (`/System/Volumes/Data`) is reported as a symlink by `openat`, fall back to skip-with-reason, never follow. |
| The extra dev-check walk in `removeEntry` slows a huge purge | Low | Capped by `MaxCheckEntries`. Directories only. Time it on a 100k-entry fixture. |
| `d.Info()` per directory in `checkContents` adds `Lstat` cost | Low | Directories only. Measure with the existing executor benchmarks if any, else a quick fixture timing. |
| The F1 test relies on `hdiutil`, unavailable in some CI | Low | `t.Skip` when missing. The device-mismatch logic also gets an injectable unit test. |
| F8 listers accept something the detector wouldn't | Low | Listers return only what the tool itself lists. Exact match. `validNativeArg` and `nativeArgRE` still apply. The plan's argv was already fixed at plan time. |
| `checkPrivateDir` uid check breaks a test that uses a store dir owned by root (e.g. `/private/tmp`) | Low | Tests use `t.TempDir()` (owned by the user). `TestRedteamHeldMalformedSandbox` already covers `/private/tmp`. |
| A fix accidentally loosens a check | Low | All `Held` tests plus existing swap and crash tests must stay green. Mutation check per fix. |

## Decision needed

- **F5, hard-linked regular files (`st_nlink > 1`)**: the report suggests possibly refusing them. **Recommendation: don't.** The strict open closes the ancestor-swap hole by itself, and some legitimate cache files are hard-linked (e.g. by package managers), so refusing would skip real cleanups. Revisit only if a later red-team shows a path the strict open misses.

## Out of scope (deferred, recorded in PHASE-6 Open)

From the report's "Notes (not findings, untested)":

1. `node_modules`/`site-packages` exemption depth (a `.git` under `…/node_modules/myproj/` below a cache target goes unchecked).
2. Purge vs restore race on one ID. Fix: per-entry `flock`, like `store.LockPlans`.
3. Crash during a DeleteNow purge can leave a damaged, never-purgeable entry. Fix: delete `payload/` first and `manifest.json` last.
4. Restore doesn't re-check the policy (needs a forged store row + manifest; never overwrites).
5. Tool binaries are resolved through `PATH` at exec time. Fix: pin absolute paths at plan time.
6. The executor trusts `approved`. Fix: a defence-in-depth check for Claude-origin and RISKY items.

Items 2 and 3 are cheap and could be a small follow-up (P6-R1b) if wanted.

## Acceptance

- [ ] All 8 `TestRedteamBroken*` tests renamed to `TestRedteamFixed*` and passing
- [ ] All 15 `TestRedteamHeld*` tests and all existing executor tests still pass
- [ ] `make check` passes (race, lint, importcheck, forbidcheck)
- [ ] `TestSchemasFrozen` passes without `-update`
- [ ] No new delete, rename or exec calls outside `internal/executor`
- [ ] Mutation check done for each fix
- [ ] P6-R1 report Resolution, SAFETY_MODEL, PHASE-6 and PHASE-9 updated
- [ ] No commit until the user asks
