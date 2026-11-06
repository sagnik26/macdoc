# 0002: Plans and tools take opportunity IDs, never paths

**Status:** Accepted. **Recorded in:** `docs/SAFETY_MODEL.md` §1 (2), `docs/DEVELOPMENT_PLAN.md` §2 rule 3.

## Context

If a command or an MCP tool accepts a path as a cleanup target, then anything that can choose a string (a user typo, a model, a file name that reaches a prompt) can choose what gets deleted. The set of things macdoc is allowed to remove is small and known: what its recipes find.

## Decision

Nothing outside the recipe catalog can be acted on. A plan item names an opportunity ID (`O-ab12cd`) that a scan produced; the path is looked up again from the store and re-validated at apply time. `planner.ParseIDs` refuses anything that looks like a path, and no CLI flag or MCP tool takes a path as a target.

## Consequences

- Unknown large items are advice-only: macdoc shows them but offers no action. A person who wants one gone removes it by hand.
- Adding a new kind of cleanup means adding a recipe (data plus tests), not a flag.
- The set of reachable targets is reviewable by reading the catalog.

## Tests

`internal/planner/tests.TestParseIDs`, `internal/planner/tests.TestNormalizeID`, `internal/mcp/tests.TestToolList`, `internal/planner/tests.TestBuildRefusals`.
