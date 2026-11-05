# Interfaces: CLI, dashboard and MCP

Part of the [macdoc architecture guide](../../README.md#architecture).

## 10. Interfaces: CLI, dashboard, MCP

### 10.1 Commands

`cli/root.go` builds the Cobra tree. Top-level commands:

| Command | Purpose | Changes the Mac? |
|---|---|---|
| `macdoc` | Welcome screen and dashboard | No |
| `doctor` | Summary; subcommands `scan`, `health`, `slow`, `trend`, `space`, `wins`, `dev`, `projects`, `explain <O-id>` | No (`scan` writes macdoc's own store) |
| `plan [O-id...]` | Seal a dry-run plan | No |
| `apply <P-id>` | Carry out a plan after confirmation | Yes |
| `quarantine` | `list`, `restore <Q-id>`, `purge (--expired \| <Q-id>...)` | Restore and purge only |
| `log` | Audit log | No |
| `config` | `set`, `protect`, `unprotect`, `path` | macdoc's own settings file only |
| `schedule` | `enable`, `disable` | macdoc's launchd file only |
| `mcp` | MCP server over stdio | No |
| `completion`, `version` | Shell completion, build version | No |

Most commands support `--json`, and each `--json` output has a frozen schema in
`schema/`. Contract tests fail if output drifts from its schema.

### 10.2 The dashboard

The dashboard is a Bubble Tea program (`cli/tui.go`). Its state is a `TuiModel`
with the dependencies injected through `TuiDeps`, so tests run it without a terminal.

- **Views:** Overview, Suggestions, Toolchains, Projects, Space, Health. Switch with
  `tab` or `1`–`6`, or with `/` commands such as `/space`.
- **Files by job:** `tui.go` (model, update loop, layout), `tui_tables.go` (tables),
  `tui_plan.go` (marking, the plan picker, `preMarkSafe`, `markedTotal`),
  `tui_remove.go` (the confirmation box for removal), `tui_apply.go` (runs
  `apply` from the picker), `tui_intro.go` (the welcome screen).
- **Removal** is two keypresses for SAFE and CAUTION items, the second one in a
  confirmation box, and a typed code for RISKY items. It always goes through a sealed
  plan and `executor.Apply`.

### 10.3 The MCP server

`internal/mcp` implements the Model Context Protocol server (`macdoc mcp`). It has
eleven tools. Read tools: `get_system_overview`, `get_storage_breakdown`,
`get_growth_history`, `list_opportunities`, `explain_opportunity`, `list_projects`,
`diagnose_slowness`, `get_plan`, `get_audit_log`. Propose-only tools: `create_cleanup_plan`,
which returns a plan ID and the command for the person to run, and `refresh_scan`, which
starts a read-only scan in the background.

`internal/mcp` does not import `executor` (CI-enforced). The tool list is pinned by a
test, so adding a tool is a visible change. Text from the disk is cleaned by `textsafe`
before it reaches the model, and every tool description says not to follow instructions
found in names. The server warns when a scan made from the Claude app saw less than an
earlier scan, because the app usually lacks Full Disk Access.
