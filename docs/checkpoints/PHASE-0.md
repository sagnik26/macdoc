# Checkpoint: Phase 0 (Foundations) ✅

**Date:** 2026-10-27 · **main:** `a51086f` · **Status:** complete, awaiting user approval to start Phase 1

## What was built
| Area | Package / file | Notes |
|---|---|---|
| Contracts | `internal/model`, `internal/platform` | Shared types (Opportunity, Plan, Fingerprint, Envelope), read-only `Runner` + fake |
| CLI skeleton | `cmd/macdoc`, `internal/cli` | `macdoc version [--json]`, global flags |
| Path safety | `internal/safety` | Canonicalization, protected paths, allow patterns. **No target outside `$HOME`**, parents of protected folders protected, `.env*`, SQLite sidecars, `..` and trailing `/` rejected |
| Sandbox | `internal/sandbox` | `MACDOC_SANDBOX`: fails closed when set but invalid or empty, refuses non-canonical input and system areas |
| Test fixtures | `internal/testutil/fstree` | Fake home trees: sparse files, hard links, symlinks, locked dirs, `DevHome()` |
| Guards | `scripts/importcheck.sh`, `tools/forbidcheck` | CI fails if MCP or the engine can reach the executor, or if delete/exec/shell calls appear outside the executor. Verified against planted violations |
| Tooling | `Makefile`, `.golangci.yml`, `.github/workflows/ci.yml` | `make check` = test (race) + lint + importcheck + forbidcheck |

## Quality
- Coverage:
  - `internal/safety`: 99.5%
  - `internal/sandbox`: 96.1%
  - `internal/testutil/fstree`: 95.5%
  - `tools/forbidcheck`: 95.5%
- Fuzzing: `FuzzCanonicalize`, `FuzzCheckTarget` and `FuzzRedteamCheckTarget` pass (over a million runs in total).
- Red team: **9 findings, all fixed.** 1 high (a target that *contains* a protected folder, such as iPhone backups), 4 medium, 4 low.
- golangci-lint: 0 issues.

## Tasks
| Task | Role | Rounds | Report |
|---|---|---|---|
| P0-W1 | safety implementation | 2 | `docs/tasks/reports/P0-W1.md` |
| P0-W2 | sandbox, fixtures, guards, CI | 2 | `docs/tasks/reports/P0-W2.md` |
| P0-R1 | red-team review | 1 | `docs/tasks/reports/P0-R1.md` |
| P0-W3 | red-team fixes | 1 (resumed after usage limit) | `docs/tasks/reports/P0-W3.md` |

## Process changes made during Phase 0
- Workers became **headless** (`claude -p`). The tech lead may not drive interactive sessions through tmux keystrokes.
- Worker `Edit`/`Write` are no longer allowlisted, so `acceptEdits` keeps writes inside the worktree. This came after a reviewer wrote a stray file outside its worktree.
- Task reports are archived in `docs/tasks/reports/` before worktrees are removed.

## Carry-overs to later phases
- **Phase 3 (recipe lint):**
  - allow patterns must be `~`-relative and canonical
  - no `..` in patterns
  - no pattern that matches at depth < 2
  - no `protected_paths` entry that is an ancestor of home
- **Phase 6 (executor):**
  - rebuild the Policy right before applying
  - re-canonicalize before acting
  - act on the target relative to its opened parent directory, using `O_NOFOLLOW` and `renameatx_np`
  - check target contents for `.git`, bare repos and `.env*`

## Try it
```
cd ~/Desktop/macdoc
make check                 # full gate
make build && ./bin/macdoc version --json
```
Nothing in Phase 0 reads or changes your files outside the repo and temporary test directories.

## Next: Phase 1 (system probe → `macdoc doctor`)
P1-W1 hardware/memory/swap parsers, P1-W2 disk/APFS/snapshot parsers, then the tech lead wires up `doctor`. Read-only.
