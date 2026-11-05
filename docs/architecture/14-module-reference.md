# Module reference

Part of the [macdoc architecture guide](../../README.md#architecture).

## 16. Module reference

| Module | Size (non-test lines) | Key files | Exported surface, briefly |
|---|---|---|---|
| `cmd/macdoc` | 13 | `main.go` | `main` |
| `cli` | ~6,200 | `root.go`, `doctor.go`, `apply.go`, `plan.go`, `log.go`, `quarantine.go`, `config.go`, `schedule.go`, `mcp.go`, `theme.go`, `tui*.go` | Command constructors; `TuiModel`, `TuiDeps`, `TuiData`, the `--json` result types |
| `engine` | ~2,500 | `overview.go`, `doctor.go`, `opportunities.go`, `storage.go`, `trend.go`, `slow.go`, `slow_tips.go`, `plan.go`, `scan.go`, `nodes.go`, `projects.go`, `state.go` | See section 5.5 |
| `executor` | ~1,600 | `executor.go` (`Apply`, `Config`, `Report`), `perform.go`, `move.go`, `native.go`, `manage.go`, `schedule.go` | `Apply`, `Restore`, `Purge`, `Config`, `Report`, `ItemResult`, `Approvals` |
| `recipes` | ~3,000 | `recipe.go`, `load.go`, `registry.go`, `detect_*.go`, `native.go`, `keep.go`, `catalog/*.yaml` | `Recipe`, `Catalog`, `Load`, detector registry |
| `store` | ~1,100 | `store.go`, `migrate.go`, `plans.go`, `quarantine.go`, `vitals.go`, `planlock.go` | `Store`, `Open`, plan and quarantine queries, `PruneScansWeekly` |
| `scanner` | ~600 | `scanner.go` | `Scan`, `Options`, `Result`, `Progress` |
| `probe` | ~870 | `disk.go`, `memory.go`, `swap.go`, `process.go`, `hardware.go`, `snapshots.go`, `types.go` | Readings for hardware, memory, swap, disk, processes and snapshots |
| `projects` | ~620 | `projects.go`, `activity.go` | `Discover`, project idle and git state |
| `planner` | ~630 | `planner.go`, `seal.go`, `actions.go` | `Build`, `Save`, `Load`, `Verify`, `Hash`, `Perform`, ID helpers |
| `safety` | ~640 | `safety.go`, `canonical.go`, `protected.go`, `match.go` | `NewPolicy`, `CheckTarget`, `IsProtected`, `Canonicalize`, `Within` |
| `precond` | ~180 | `precond.go` | `Checker`, `Known` |
| `quarantine` | ~250 | `quarantine.go` | `Root`, `EntryDir`, `ManifestPath`, entry states |
| `audit` | ~190 | `audit.go` | `Record`, `Append`, `Read`, event names |
| `config` | ~230 | `config.go` | `Load`, `Policy`, settings |
| `sandbox` | ~180 | `sandbox.go` | `Root` (MACDOC_SANDBOX checks) |
| `mcp` | ~680 | `server.go`, `tools.go`, `sanitize.go` | `New`, `Options`, the tool list |
| `textsafe` | ~140 | `textsafe.go` | Cleaning for display and model use |
| `model` | ~200 | `model.go` and related | Opportunities, plans, items, sizes, origins |
| `platform` | ~190 | `runner.go`, `fake.go`, `tool.go` | `Runner`, `FakeRunner`, `ResolveTool` |
| `platform/darwin` | ~50 | `purgeable_cgo.go`, `purgeable_stub.go` | Purgeable-space shim |
| `fsread` | ~65 | `fsread.go` | Capped, non-blocking file reads |
| `testutil/fstree` | ~100 | `fstree.go` | Fake home trees for tests |
| `tools/forbidcheck` | 13 | `main.go` | Command wrapper |
| `tools/forbidcheck/check` | ~600 | `check.go`, `run.go` | `Run`, the source checker |
