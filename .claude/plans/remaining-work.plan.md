# Plan: Remaining work after the test-layout migration

**Source:** Conversation, 2026-11-05. Current `main` is `9a8705b` (test layout merged).
**Selected Milestone:** Close the test-layout follow-ups, correct the docs, record the Phase 7 sign-off, then Phase 11.
**Complexity:** Small for the first three groups, Large for Phase 11 (several items need product decisions).

## Summary
The test-layout migration is merged and passing. What's left is small follow-up work from that migration, a stale docs note about Phase 10, the Phase 7 dogfood sign-off, and Phase 11 (open-source readiness), which the plan marks as "later". Phase 11 should not start until you pick its scope.

## Status check (verified in code, 2026-11-05)
- Phase 10 cleanup picker: **done**. `preMarkSafe` (`internal/cli/tui_plan.go:43`) and `markedTotal` (`internal/cli/tui_plan.go:78`) exist. The Phase 10 note in `docs/DEVELOPMENT_PLAN.md` ("picker work is what's left") is out of date.
- `golangci-lint` is installed (`/opt/homebrew/bin/golangci-lint`). `make lint` has never been run on the migrated code.

## Patterns to Mirror
| Category | Source | Pattern |
|---|---|---|
| Lint | `Makefile:26` | `make lint` runs `golangci-lint run ./...` |
| Safety guards | `Makefile` `importcheck` / `forbidcheck` | Run after every change; both must pass |
| Docs | `AGENTS.md` ("Source of truth") | Update the doc in the same change when a design changes |
| Tests | `internal/*/tests/*_test.go` | External `package tests`; export only what tests need |

## Tasks

### Group A: Follow-ups from the test-layout migration
1. **Run lint.** `make lint`. Fix any findings the migration caused (e.g. exported test helpers, unused code). Validate: `make lint` passes.
2. **Review the exported surface.** About 40 identifiers became exported so tests could reach them (listed in `.claude/plans/tests-layout.plan.md`). For each, decide: keep exported, or return it to unexported behind a test-only seam. Start with `executor.TestHook`, which is a mutable hook that changes apply behaviour. Proposed: pass it through `executor.Config` instead. Validate: `make test`, `make forbidcheck`, and the executor tests still pass with the same test count.
3. **Fix coverage reporting.** Tests in `tests/` don't count toward the package they test, so `go test -cover ./internal/x` reports 0%. Proposed: add `-coverpkg=./...` to a `make cover` target and document it in `AGENTS.md`. Validate: `make cover` reports ~87% total.
4. **Clean up git.** Drop the stash `tests-layout WIP: renamer broke cli production code`. Delete the remote branch `tests-layout` (already merged). Validate: `git stash list` is empty; `git ls-remote origin tests-layout` returns nothing.
5. **Record the migration in `AGENTS.md`.** Add the `tests/` layout rule, plus the two exceptions (`internal/cli/tests` in importcheck, `tools/forbidcheck/check`). Validate: docs match `scripts/importcheck.sh` and the directory tree.

### Group B: Docs drift and the Phase 7 sign-off
6. **Correct the Phase 10 note** in `docs/DEVELOPMENT_PLAN.md` and the matching line in `AGENTS.md`: the picker's pre-mark and live total are built. Validate: grep shows no "picker work is what's left".
7. **Record the Phase 7 dogfood result.** Needs your input: is the dogfood week complete, and did it pass? Then update the status line in `README.md` and `AGENTS.md`. Validate: the status lines agree with each other and with `docs/checkpoints/`.

### Group C: Phase 11 open-source readiness (needs your decision first)
8. **Privacy statement** (no network). Confirm first: the statement must match the code. Validate: grep the code for network calls (`net/http`, `url`, `dial`) and state only what's true.
9. **Recipe contribution guide.** Based on `docs/RECIPE_CATALOG.md` §3 (positive, look-alike and precondition tests per recipe). Validate: an example recipe passes the checklist.
10. **More generic thresholds.** Replace hard-coded per-machine numbers with settings or measured values. Needs a scope decision: which thresholds, and whether the config file covers them.
11. **Homebrew tap, signing and notarization.** These need your Apple developer account and a release decision. Not started; nothing to build until you confirm the account and release plan.

## Validation (every code change)
```bash
make test
make lint
make importcheck
make forbidcheck
```

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Moving `TestHook` into `Config` changes the executor's safety surface | Medium | Keep the same checks; run the executor tests with the same count; ask before merging |
| Lint finds many issues from the migration | Medium | Fix in small commits; no behaviour changes in lint-only commits |
| Phase 7 sign-off recorded without a real dogfood week | Low | Ask you to confirm the result; do not infer it |
| Phase 11 started without a scope | High | Blocked on your decisions for items 8–11 |

## Acceptance
- [ ] `make test`, `make lint`, `make importcheck`, `make forbidcheck` all pass
- [ ] No stale status claims in `README.md`, `AGENTS.md` or `docs/DEVELOPMENT_PLAN.md`
- [ ] Phase 7 status recorded from your confirmation
- [ ] Phase 11 items started only after your decisions

## Questions for you
1. Should `executor.TestHook` move into `executor.Config`? (Recommended: yes.)
2. Is the Phase 7 dogfood week complete, and did it pass?
3. For Phase 11: which items are in scope now, and do you have an Apple developer account for signing?

**WAITING FOR CONFIRMATION** before any code is written.
