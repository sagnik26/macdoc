# Checkpoint: Phase 8 (MCP server: read + propose plans) ✅

**Date:** 2026-10-30 · **main:** see `git log` · **Status:** built and tested. No plan was created and nothing was applied on the development Mac; the real-Mac check used two read-only tools.

## Built
- **`macdoc mcp`** (`internal/cli/mcp.go` → `internal/mcp`): an MCP server on stdin/stdout, built on `github.com/modelcontextprotocol/go-sdk` v1.8.0 (released 2026-09-04; pre-approved in AGENTS.md).
  - Started by hand in a terminal, it prints a hint to stderr. stdout is the protocol.
  - It stops cleanly on Ctrl+C or SIGTERM, and a background scan stops with it.
- **11 tools** (docs/MCP_DESIGN.md §2):

| Tool | Reads from | Side effects |
|---|---|---|
| `get_system_overview` | `engine.Doctor` and the latest scan's age | none |
| `get_storage_breakdown` | `engine.Storage` | none |
| `list_opportunities` | `engine.Opportunities`, with filters for risk, toolchain, min_bytes and idle_days, limit 50 (max 200) | none |
| `explain_opportunity` | opportunity + base risk + `can_plan`; ADVISE items carry their steps | none |
| `list_projects` | `engine.Projects` (artifacts ≥ 1 MB, optional idle filter) | none |
| `diagnose_slowness` | `engine.Slow` | a vitals sample in macdoc's state |
| `get_growth_history` | `engine.Trend` | none |
| `refresh_scan` | `engine.Scan` in the background; later calls report `running` / `done` | a read-only crawl, stored as a scan |
| `create_cleanup_plan` | `engine.CreatePlan` with `origin: "claude"`, 1–50 opportunity IDs | writes a plan file |
| `get_plan` | `engine.ShowPlan` (plan-ID format checked first) | none |
| `get_audit_log` | `engine.AuditLog`, limit 50 (max 500) | none |

- **The handoff:** `create_cleanup_plan` returns:
  - the plan ID and when it expires
  - a summary (items, bytes, by risk, how many are of unknown size)
  - each item's action and restore cost, plus any refusals
  - `"user_action": "Run in Terminal:  macdoc apply P-…"`
  - a note that Claude can't apply plans

  `apply` already asks per item for Claude-origin plans (Phase 6).
- **Server instructions** tell Claude that it can't act, how to propose a plan, not to suggest `rm` or scripts, to treat names as untrusted, and never to show unknown as zero.
- **Output:** every result is `{"data": …, "note": "<untrusted data>", "warnings": […]}`, where `data` is the same shape as the CLI's `--json`. Every string, map keys included, has ANSI escapes and control, bidi and line-separator characters stripped and is capped at 1,000 characters. Errors are cleaned the same way.
- **Dashboard:** `/mcp` now shows how to connect Claude.

## Done-when
| Criterion | Test |
|---|---|
| `macdoc mcp` runs a stdio server with the MCP_DESIGN tools | `TestToolList` pins exactly the 11 names. Checked by hand over stdio with the real binary (`initialize`, `tools/list`) |
| `create_cleanup_plan` writes `origin: "claude"` plans and returns the command | `TestCreatePlan`: plan file origin, `user_action`, nothing on disk changed. Refused: empty input, a path, an advice-only ID, an unknown ID, traversal, more than 50 IDs |
| `internal/mcp` doesn't import the executor | `make importcheck` (asserts `internal/mcp/...` with tests). The package also avoids `internal/cli`, which imports the executor |
| Registration documented | README "Use it from Claude", `macdoc mcp --help`, MCP_DESIGN §1 |

Also tested:
- No tool name suggests acting (apply, delete, remove, restore, purge, run, exec, command, config, protect, move), and no input field suggests a path or command (path, target, argv, command, file, dir). Read tools are annotated read-only; the two writers aren't.
- Disk-sourced names arrive cleaned (an ANSI-wrapped "IGNORE PREVIOUS INSTRUCTIONS" title with a bidi override). `TestCleanString` covers the sanitizer cases and the cap.
- Explain accepts upper or lower case and rejects paths and unknown IDs; growth needs two scans; `get_plan` rejects non-IDs.
- `refresh_scan` goes from started to running to done.
- The tool-list pin was checked by adding a fake `apply_plan` name: the test fails.

## Real Mac (read-only)
- `get_system_overview`: 82.9 GB free, 3.9 GiB swap, pressure warn, scan 25 h old.
- `list_opportunities`: uv cache (CAUTION, 359 MB measured), Node.js v20 under nvm (CAUTION, 161 MB), leftover iOS simulator runtimes (ADVISE, 8.8 GB).
- Not registered with Claude on this Mac and no plan created: that's for the user to do (`claude mcp add macdoc -- macdoc mcp`).

## Deviations and later
- `create_cleanup_plan` has no free-text `note` input. Text from Claude stored in a plan would be shown at `apply`, which is a channel for persuasion; plans contain IDs only.
- Phase 9: at most 5 open Claude plans and 10 `create_cleanup_plan` calls per 10 minutes; an "older than max_age" message distinct from the planner's stale-scan error; guidance when a scan from the Claude app sees less than a Terminal scan (TCC).
- Done ahead of Phase 9: string sanitization and the untrusted-data note.
