# S2: mutation testing the code that guards deletion

**Question.** Coverage says which lines ran, not whether a wrong implementation would be caught. Mutation testing changes the code (flips a `>` to `>=`, drops a condition, ignores an error) and checks that some test fails. A mutant that survives is a specific missing test, or an equivalent change that no test can see.

**Tool.** [`go-mutesting`](https://github.com/avito-tech/go-mutesting), run on a **copy** of the repository (never on the working tree) with `exec.sh`, which runs the mutated package's own `tests/` directory. This repository keeps tests in `internal/<pkg>/tests/`, so the tool's default exec script, which runs the package directory, would see no tests.

```sh
go install github.com/avito-tech/go-mutesting/cmd/go-mutesting@latest
mkdir copy && tar --exclude=.git -cf - . | tar -xf - -C copy && cd copy
go-mutesting --exec="$PWD/../exec.sh" --exec-timeout=120 --verbose ./internal/safety/...
python3 -I survivors.py <log> safety          # list survivors with file:line and the change
```

`exec_wide.sh` also runs the engine, MCP, CLI and executor tests, to separate "no test anywhere" from "no test in this package". In `go-mutesting` output `PASS` means the mutant was **killed** and `FAIL` means it **survived**.

## Results (2026-11-06, `60a0165` plus the module rename)

Kill rate = killed / (killed + survived). Mutants that do not compile are skipped.

| Package | Before | After the new tests | Left |
|---|---:|---:|---|
| `internal/precond` | 39 / 65 = 60.0% | 62 / 65 = **95.4%** | 3, all equivalent |
| `internal/safety` | 145 / 177 = 81.9% | 158 / 177 = **89.3%** | 19, mostly equivalent or fail-closed |
| `internal/planner` | 84 / 178 = 47.2% | not yet addressed | 94 (a wider test set killed about 10 more of the 67 re-run so far) |
| `internal/executor` | 32 / 123 = 26% at 157 of an estimated ~300 mutants (run not finished) | accounting tests added, not yet re-measured | not yet known |

New tests: `internal/safety/tests/reasons_test.go`, `internal/precond/tests/branches_test.go`, `internal/executor/tests/report_test.go`.

## What the survivors showed

- **`git_clean` was untested.** The precondition that stops cleanup of a project with uncommitted changes (`precond.gitClean`, 0% coverage) could be replaced by a no-op and every test passed. It is now tested for clean, dirty, untracked, git failing, no repository, `ios/` and `android/` build folders, and nested repositories.
- **Tests asserted "protected" but not why.** `IsProtected` tests checked the boolean and a non-empty reason, so changing which rule fires (`/` reported as an ancestor of home, `/Users` reported as outside home) survived. Tests now pin the exact reason for each branch.
- **Error paths were not checked for their content.** `NewPolicy` failures were asserted as `err != nil` only. A mutant that kept going after the error often produced a different error, so the test still passed. They now assert the cause.
- **Report totals were unpinned.** `n += it.MovedBytes` could become `n -= it.MovedBytes` or `n = it.MovedBytes` with no test failing. Those totals are what a person reads after an apply and what the audit log records.
- **Equivalent mutants are real.** For example, every system root is a single path component, so `!r.system && len(pc) < len(r.comps)` at `safety/protected.go:140` cannot change behavior. These are listed as equivalent, not tested. A surviving mutant is not automatically a bug.

## Not done

- Executor survivors, mostly `Restore` and `Purge` error branches (`manage.go:65-127`) and `Apply` early returns (`executor.go:193-221`). Each needs a fixture that triggers that failure.
- Planner survivors (`seal.go`, `planner.go`, `actions.go`).
- The pre-registered targets (safety and planner at least 90%, executor at least 85%) are met only for precond.
- Equivalent-mutant classification is by reading the code and is not machine-checked.
