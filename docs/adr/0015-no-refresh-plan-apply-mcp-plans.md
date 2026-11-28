# 0015: No `refresh`, no `apply` or `/plan`, and no MCP plan tools: macdoc cleans, asks, or only suggests

**Status:** Accepted (owner decision, 2026-11-24). Implemented. It supersedes the command set of [ADR 0012](0012-five-commands-and-one-apply.md) and retires [ADR 0009](0009-privileged-maintenance-steps.md) (its general rules now live in [ADR 0014](0014-reclaim-system-cleanup.md)). **Recorded in:** `internal/cli`, `internal/mcp`, `internal/recipes/catalog`, `internal/executor/privileged.go`, `AGENTS.md`, `docs/SAFETY_MODEL.md`.

## Context

macdoc had three ways to get a cleanup done: `reclaim`, the dashboard's Enter on a suggestion, and a separate plan-then-apply path (`macdoc apply`, `/plan`, `/apply`, the `p` and `a` keys, a plan overlay, and the MCP tools `create_cleanup_plan` and `get_plan`). Next to them sat `refresh`, a page of maintenance steps (flush DNS, free memory, reset Quick Look) that free no space and need a password. People found the extra path and the refresh page confusing. The owner's rule is simple: macdoc either deletes on its own (SAFE and CAUTION, `/reclaim`), asks before it deletes, or only suggests.

## Decision

1. **`refresh` is removed:** the command, the `/refresh` page, the welcome-screen entry, the `maintenance.*` recipes and detector, the `on_request` and `privileged` recipe fields, `recipes.PrivilegedArgvAllowed` and the executor's native privileged path (`Config.Priv`, `realPrivileged`).
2. **`macdoc apply`, `/plan`, `/apply`, the `p` and `a` keys and the plan overlay are removed.** A plan still exists inside every removal: it is sealed, verified and re-checked by the executor, because that is how the safety checks run (ADR 0002, 0003). It is no longer something a person or an assistant creates, lists or applies by name.
3. **The MCP server loses `create_cleanup_plan` and `get_plan`.** Claude can diagnose and suggest; the person cleans with `macdoc reclaim` or the dashboard. The tool list stays pinned by `internal/mcp/tests.TestToolList`.
4. **`sudo` stays, for one thing.** `internal/executor/privileged.go` still runs the fixed commands of the `system` method (ADR 0014), with the rules ADR 0009 set: a real terminal, no sandbox, the password asked by the system, `sudo -k` after, never `sudo -S`, never from a Claude plan, its own y/N for each item. The three-command allow-list of ADR 0009 is gone.
5. **RISKY items** are asked about one by one, with the item's ID typed, in `macdoc reclaim` and in the dashboard (Enter on a suggestion, or `d` on the `/reclaim` result screen). They are never part of an automatic run.

## Consequences

- One fewer way to delete, and no administrator step outside system cleanup. The surface that can reach root is smaller than before.
- The Claude-origin plan guards in the planner and executor stay as defense in depth, although no tool can now create such a plan.
- `macdoc apply --yes` never existed; `macdoc reclaim --tier safe|caution --yes` and the dashboard's `/reclaim` remain the only no-prompt cleaners (ADR 0014 §5).
- The JSON schemas `apply`, `apply-list`, `apply-show` and `refresh` are deleted. This is a breaking change for anyone who scripted them.

Tests: `internal/cli/tests.TestCommandSet`, `internal/mcp/tests.TestToolList`, `internal/mcp/tests.TestRedteamHeldToolsDontTouchTheMac`, `tools/forbidcheck/check/tests.TestSudoOnlyInThePrivilegedFile`.
