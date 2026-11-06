# 0003: Only `internal/executor` may change files or run mutating commands

**Status:** Accepted. **Recorded in:** `docs/DEVELOPMENT_PLAN.md` §2 rule 2, `docs/SAFETY_MODEL.md` §4.4.

## Context

A deletion bug is worst where it is hardest to notice. If any package can call `os.RemoveAll` or run a command, the whole codebase must be audited for every change. Confining those calls to one package makes one package the thing that needs the strictest review and the strongest tests.

## Decision

Calls that delete, move, truncate or run commands are allowed only in `internal/executor`. `internal/mcp`, `internal/engine` and `internal/planner` must never import it. Two checks enforce this in CI: `scripts/importcheck.sh` (the import graph) and `tools/forbidcheck` (a source scan for forbidden calls, with an explicit allow-list). Commands are never run through a shell, with `sudo`, or with strings assembled at run time: argv comes from a recipe.

## Consequences

- Reviewing a change that can delete means reviewing `internal/executor`.
- Code elsewhere that needs to run a read-only tool goes through `platform.Runner`, which is limited to read-only use.
- The import and forbidden-call guards are enforced by tools, not by types. `executor.Apply` takes a plain `model.Plan`, but it calls `planner.Verify` itself, unconditionally, against its own store, catalog and a freshly built policy (`executor.go`), so a caller cannot skip verification. A `Verified` token returned by `Verify` and required by `Apply` was considered (plan.md, claim 2) and not adopted: it would give compile-time evidence but no protection the code lacks, and would change `Apply`'s signature for 2 production and 23 test callers. `internal/executor/tests.TestRedteamHeldForgedPlans` and `internal/planner/tests.TestForgedPlans` show forged plans being refused.

## Tests

`tools/forbidcheck/check/tests.TestForbidcheckScript`, `internal/executor/tests.TestMutatingCallSites`.
