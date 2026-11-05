# Safety layers

Part of the [macdoc architecture guide](../../README.md#architecture).

## 9. Safety layers

macdoc does not rely on one check. A cleanup has to get through all of these:

| Layer | Where | What it stops |
|---|---|---|
| Source policy | `make forbidcheck` | Any delete, rename, truncate or command outside `executor` |
| Import graph | `make importcheck` | `engine`, `mcp` or `planner` reaching the executor |
| Recipes only | `recipes`, `planner.Build` | Anything not described by a reviewed recipe |
| Protected paths | `safety` | Documents, Desktop, iCloud, Mail, Messages, Photos, Keychains, `.ssh`, and similar; user additions only add protection |
| Canonical paths | `safety.Canonicalize` | Symlink and `..` tricks; the path is resolved before comparison |
| Sealed plans | `planner.Verify` | Edited or expired plans, and plans from another catalog |
| Single apply | `plans.status` | Applying the same plan twice |
| Preconditions | `precond` | Acting while the owning app runs or a daemon is unreachable |
| Re-check at act time | `move.go`, `native.go` | Anything that changed since the plan was made |
| Human confirmation | `cli`, dashboard | Any removal without a typed confirmation at a real terminal; RISKY items need a typed code |
| Audit | `audit` | Every outcome is recorded before and after |
| Recovery | `quarantine` | Removed items can be restored until they expire |
| Sandbox | `sandbox` | Tests cannot touch the real home folder |

The invariants behind these layers are listed in `AGENTS.md`.
