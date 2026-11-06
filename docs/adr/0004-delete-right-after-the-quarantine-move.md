# 0004: A quarantine move is deleted right away so the space is freed

**Status:** Accepted. Owner decision of 2026-10-28; originally items were held in quarantine for 7 days. **Recorded in:** `README.md` ("Decisions locked"), `docs/SAFETY_MODEL.md` §1 (6) and §4.6.

## Context

Holding removed folders in quarantine for a week meant a cleanup did not free the space it was run for, which defeats the purpose on a Mac that is short of disk. The move into quarantine does, however, make a crash recoverable: the folder is either still in place or fully in quarantine.

## Decision

When no native tool command exists, the executor moves the folder into macdoc's quarantine with a manifest and then purges the entry immediately (the `DeleteNow` path), through the same checks as `quarantine purge`. If the purge is refused, the item stays restorable in quarantine. Every removal is labelled "Not undoable by macdoc" together with its restore cost (rebuild, re-download, irreplaceable).

## Consequences

- Space is freed at once. `quarantine restore` is for the cases where something was left behind, not a general undo.
- Safety rests on the checks before the move (allow-list, protected paths, fingerprint, preconditions) and on the person's confirmation, not on a way back. That is why mutation testing of `safety`, `planner`, `precond` and `executor` matters more than usual.
- The restore cost shown for each suggestion has to be accurate; it is part of the person's decision.

## Tests

`internal/executor/tests.TestCrashMidApply`, `internal/executor/tests.TestRedteamFixedPurgeCrashOrder`, `internal/executor/tests.TestRestoreNeverOverwrites`.
