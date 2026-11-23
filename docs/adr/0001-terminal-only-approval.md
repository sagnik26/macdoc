# 0001: Only a person at a real terminal approves a removal

**Status:** Accepted. **Recorded in:** `docs/SAFETY_MODEL.md` §1 (4) and §4.2.

## Context

macdoc deletes files. The same engine is also exposed to Claude over MCP, and text on disk (file names) can try to steer an AI client. A removal that any program, script or model could confirm would make every other safeguard advisory.

## Decision

A removal needs a person at a real terminal. `macdoc apply` refuses when stdin or stdout is not a TTY (checked with `isatty`, so pipes and `/dev/null` are refused) and asks the person to type the plan's four-character code. RISKY items, and every item of a plan that Claude proposed, are confirmed one by one with a default of No. There is no `--yes` and no non-interactive bypass. The dashboard's remove (Enter, then Enter again, with typed IDs for RISKY items) re-checks the terminal the same way.

## Consequences

- macdoc cannot be scripted into unattended cleanup. Scheduled work (`macdoc schedule`) only scans.
- Claude proposes plans and the person applies them in Terminal. The MCP server has no tool that applies.
- Approval cost is real: cleaning many items takes several confirmations. The dashboard's two-step confirm for SAFE and CAUTION items was a deliberate relaxation chosen by the owner on 2026-10-28; RISKY items still need typed IDs.

## Tests

`internal/cli/tests.TestApplyNeedsATerminal`, `internal/cli/tests.TestApplyRiskyAsksPerItem`, `internal/mcp/tests.TestToolList`, `internal/cli/tests.TestTUIRemoveEnterTwice`.
