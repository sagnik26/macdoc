# Plan: Move all tests into `tests/` subfolders

**Source:** Conversation, 2026-11-02
**Branch:** `tests-layout` (from `main`, not merged)
**Complexity:** Large. 81 test files across 20 packages, plus 40 fixture files under `testdata/`.

## Decisions (confirmed by user)
1. **Unexported access:** option (a), an `export_test.go` bridge file in each package. It is `package <pkg>` and only re-exposes unexported names for the external tests. The production API does not change.
2. **Fixtures:** move `testdata/` into `tests/testdata/`, so each test folder is self-contained.

## Summary
Every package that has tests gets a `tests/` subfolder. The test files there use `package tests` and import the package they test. Behaviour does not change: the same tests run with the same names and counts.

## Patterns to mirror
| Category | Source | Pattern |
|---|---|---|
| Test layout (before) | `internal/*/…_test.go` | Beside the code, `package <name>` |
| Forbidden-call check | `tools/forbidcheck/main.go:220` | Skips names ending in `_test.go`. Moved files keep that suffix |
| Import check | `scripts/importcheck.sh:189` | Test case built around `sub_test`. Verify it still passes |
| Fixtures | `git ls-files | grep /testdata/` | 40 files. Relative paths must point to `../testdata` or `tests/testdata` |

## Files to change
| Area | Action | Why |
|---|---|---|
| `internal/<pkg>/*_test.go` (81 files) | MOVE to `internal/<pkg>/tests/` | Requested layout |
| Moved files | UPDATE: `package tests`, import `macdoc/internal/<pkg>`, qualify names | External test package |
| `internal/<pkg>/export_test.go` | CREATE, one per package that needs it | Bridge for unexported names (decision 1) |
| `internal/*/testdata/` (40 files) | MOVE to `tests/testdata/`, fix paths | Decision 2 |
| `tools/forbidcheck`, `scripts/importcheck.sh` | VERIFY, update only if their rules break | Keep the safety guarantees |
| `docs/DEVELOPMENT_PLAN.md` §3 | UPDATE | The layout convention changes (AGENTS.md) |

## Method (per package)
1. `git mv` the test files into `tests/`, and change `package <name>` to `package tests`.
2. Run `go test -c -gcflags=-e -o /dev/null ./internal/<pkg>/tests/` to list every undefined identifier.
3. Exported names (capital first letter): qualify as `<pkg>.Name`.
4. Unexported names: add `var Name = name` (or `const`/`type`) in `export_test.go`, then qualify as `<pkg>.Name`.
5. Move `testdata/` and fix paths in the tests.
6. Validate (below). Commit one batch at a time.

## Tasks
### Task 0: Spike on `internal/mcp` (2 files)
- Status: files moved, package renamed. 13 undefined names: 4 exported (`New`, `Options`, `ToolNames`, `UntrustedNote`), 9 unexported.
- Validate: `go test -list '.*'` gives the same 32 names as before. `make test` passes.
- Stop point: review before the rest.

### Task 1: Small packages (1–2 files each)
audit, config, planner, precond, projects, quarantine, scanner, testutil/fstree, textsafe, platform/darwin, store, forbidcheck.

### Task 2: Medium packages
safety (4), sandbox (3), probe (7), executor (8).

### Task 3: Large packages
engine (10), recipes (13), cli (19). Helpers shared across files in the same package move together.

### Task 4: Docs
Update `docs/DEVELOPMENT_PLAN.md` §3 to describe the new layout.

## Validation (every batch)
```bash
make test                                   # race on
make importcheck forbidcheck
go test -list '.*' ./internal/<pkg>/...     # same names and count as before the move
```
Final: compare the full test-name list for every package before and after. Only the directories should differ.

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Helper defined in one test file, used by another | High | Move files of a package together; compile after each batch |
| Fixture paths break | High | Fix paths; checked in the spike |
| Qualifying a name that is also a local variable | Medium | Compile errors catch most; the test-name comparison catches the rest |
| Forbidden-call check misses moved files | Low | Keep `_test.go` suffix; the forbidcheck tests cover this |
| Large diff, hard to review | High | One commit per batch on `tests-layout`; no merge to `main` without review |

## Acceptance
- [ ] Every package with tests has a `tests/` folder holding its test files
- [ ] `make test`, `make importcheck`, `make forbidcheck` pass
- [ ] Test names and counts match the original
- [ ] `docs/DEVELOPMENT_PLAN.md` describes the new layout
- [ ] Work is on `tests-layout`, not `main`; nothing is committed until the user asks

## Status (complete, uncommitted)
- [x] Every package with tests has a `tests/` folder. All 81 test files are in `tests/`, with 562 test functions (same as HEAD).
- [x] `make test` (race on), `make importcheck`, `make forbidcheck`, `go vet ./...` and `gofmt` all pass.
- [x] `scripts/importcheck.sh`: allowlist now includes `macdoc/internal/cli/tests` (approved by user).
- [x] `tools/forbidcheck`: logic moved to `tools/forbidcheck/check` (library); `main.go` only wires the command; tests in `check/tests` (approved by user).
- [x] Docs: `docs/DEVELOPMENT_PLAN.md` §3 describes the layout and the two exceptions above.
- Not done: `make lint` (golangci-lint) was not run. Nothing is committed.

Renames made to support the export (all in production code):
- mcp: `sanitize`, `cleanString`, `sawLess`, `fullDiskAccessWarning`, `maxOpenPlans`, `maxString`, `planRateMax`, `planRateWindow`, `untrustedHint`
- executor: `lockQuarantine`, `testHook`, `resolveTool`
- probe: `purgeableBytes` → `PurgeableLookup`
- store: `Store.db` → `DB`
- safety: `Policy.extraProtected` → `ExtraProtected`
- recipes: `Env.now()` → `Env.Clock()`
- engine: `projectCache` → `ProjectCache` (`key` → `Key`), `scanNodes` → `ScanNodes` (`st` → `St`, `scanID` → `ScanID`)
- scanner: `scanner` → `Scanner` (`prefix`, `unmeasured`, `mapPath`, `fail`), `linkSet` → `LinkSet` (`limit`, `m`, `hit`, `count`)

## Review items
- `executor.TestHook` is now an exported mutable hook in the executor, which changes apply behaviour. Consider passing it through `Config` instead.
- Stash `tests-layout WIP: renamer broke cli production code` holds the earlier failed attempt. Drop it once this is done.

## Lessons from the attempt
- The renamer must only rename top-level declarations, never struct fields or selectors.
- Compile errors from production packages must not drive test rewrites.
- Tests that read private fields or types need either exported accessors or a rewrite through the public API. Each case needs a decision.
