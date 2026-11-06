# Architecture decision records

Short records of decisions that shape macdoc, written so that someone who was not there can see what was decided, why, and what it costs. Each already existed as a line in `README.md`, `docs/SAFETY_MODEL.md` or `docs/DEVELOPMENT_PLAN.md`; these add the reasoning, the consequences and the current status.

An accepted record is changed only by a new record that supersedes it.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-terminal-only-approval.md) | Only a person at a real terminal approves a removal | Accepted |
| [0002](0002-ids-not-paths.md) | Plans and tools take opportunity IDs, never paths | Accepted |
| [0003](0003-executor-is-the-only-mutator.md) | Only `internal/executor` may change files or run mutating commands | Accepted |
| [0004](0004-delete-right-after-the-quarantine-move.md) | A quarantine move is deleted right away so the space is freed | Accepted (owner decision, 2026-10-28) |
| [0005](0005-size-by-allocated-blocks.md) | Sizes come from allocated blocks (`st_blocks`) | **Under review**: see S1 |
| [0006](0006-tests-in-a-tests-folder.md) | Each package keeps its tests in `internal/<pkg>/tests/` | Accepted; layout under review |
