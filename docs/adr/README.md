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
| [0007](0007-discovered-software-is-advisory.md) | Discovered software is advisory until a recipe matches it | Accepted (owner decision, 2026-11-13) |
| [0008](0008-app-leftovers-use-the-normal-removal-path.md) | Removing an app's leftover data uses the normal removal path | Accepted (owner decision, 2026-11-14); partly implemented |
| [0009](0009-privileged-maintenance-steps.md) | Privileged maintenance steps run only through the OS password prompt, from an exact allow-list | Accepted (owner decision, 2026-11-13) |
| [0010](0010-slimming-universal-apps.md) | macdoc reports slimmable universal apps and never removes slices or re-signs apps | Accepted (experiment S6, 2026-11-14) |
| [0011](0011-app-bundles-and-support-folders.md) | App bundles directly in `/Applications` and Application Support leftovers may be moved, through one exact pattern and RISKY recipes | Accepted (owner decision, 2026-11-14) |
| [0012](0012-five-commands-and-one-apply.md) | The CLI is bare `macdoc` plus `reclaim`, `retire`, `refresh`, `explore`, `vitals` and `scan`, with one `apply` that plans, confirms and applies; `doctor` and `plan` are removed | Accepted (owner decision, 2026-11-14) |
