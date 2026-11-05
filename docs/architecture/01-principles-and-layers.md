# Principles and layers

Part of the [macdoc architecture guide](../../README.md#architecture).

## 1. Principles

- **Two paths that never mix.** The read path measures and explains; it never changes
  the Mac. The write path acts only on a sealed plan that a person confirmed, and only
  through `internal/executor`.
- **IDs, not paths.** Users and Claude refer to suggestions by opportunity ID (`O-…`),
  plans by `P-…`, and quarantine entries by `Q-…`. No command or tool accepts a
  path as a cleanup target.
- **Every number says where it came from.** Sizes are *measured* (we read them),
  *tool-reported* (a command told us), *estimated*, or *unknown*. Unknown is never
  shown as zero.
- **Small, guarded packages.** Two checks, run in CI, keep the graph honest: one for
  imports, one for dangerous calls.
- **Testable seams.** Anything that touches macOS goes through an interface
  (`platform.Runner`, the executor's `StageHook`, the dashboard's `TuiDeps`), so tests
  run against fakes and fixture trees.

## 3. Dependency graph and layers

These are the real imports between macdoc packages (from `go list`, 2026-11-06).

| Package | Imports (macdoc only) |
|---|---|
| `cmd/macdoc` | `cli` |
| `cli` | `audit`, `config`, `engine`, `executor`, `mcp`, `model`, `planner`, `platform`, `projects`, `quarantine`, `recipes`, `safety`, `scanner`, `store`, `textsafe` |
| `mcp` | `engine`, `model`, `planner`, `platform`, `projects`, `recipes`, `store`, `textsafe` |
| `engine` | `audit`, `config`, `model`, `planner`, `platform`, `probe`, `projects`, `quarantine`, `recipes`, `safety`, `scanner`, `store` |
| `executor` | `audit`, `config`, `model`, `planner`, `platform`, `precond`, `quarantine`, `recipes`, `safety`, `sandbox`, `store` |
| `planner` | `fsread`, `model`, `recipes`, `safety`, `store` |
| `recipes` | `fsread`, `model`, `platform`, `projects`, `safety` |
| `precond` | `model`, `platform`, `projects`, `recipes` |
| `probe` | `model`, `platform`, `platform/darwin` |
| `projects` | `fsread`, `model`, `platform`, `safety` |
| `quarantine` | `fsread`, `model`, `store` |
| `config` | `safety` |
| `sandbox` | `safety` |
| `scanner`, `store` | `model` |
| `audit`, `safety`, `model`, `platform`, `fsread`, `textsafe`, `testutil/fstree` | none (leaves) |

Layers, from the bottom up:

1. **Leaves:** `model`, `safety`, `platform`, `fsread`, `textsafe`, `audit`.
2. **Facts about the Mac:** `probe`, `scanner`, `projects`, `store`.
3. **Knowledge:** `recipes` (what can be cleaned), `precond` (when it's safe to act).
4. **Decisions:** `planner` (what a person may do, sealed), `config`.
5. **Analysis façade:** `engine`.
6. **Interfaces:** `cli`, `mcp`.
7. **The only mutation:** `executor`, with `quarantine` (read side) and `sandbox` (test confinement).

Two rules make the graph safe and are enforced by `scripts/importcheck.sh` in CI:

- `engine`, `mcp` and `planner` must never depend on `executor`. Claude and the analysis
  code therefore have no path to a deletion.
- Only `cli`, `cli/tests` and `cmd/macdoc` may depend on `executor`.

`scripts/forbidcheck.sh` (through `tools/forbidcheck`) parses the Go source and fails if
any file outside `internal/executor` deletes, renames, truncates or runs a command.
