# macdoc: Build Strategy

The diagram in §1 is the current binary. The phase table in §2 uses the command names from when each phase was built; [ADR 0012](adr/0012-five-commands-and-one-apply.md) is the current set (`reclaim`, `retire`, `refresh`, `explore`, `vitals`, `scan`, `apply`).

## 1. One engine, two front doors

macdoc is **one Go binary** with two ways in. Both sit on the same engine.

```
                        ┌──────────── one Go binary: macdoc ────────────┐
                        │                                               │
 You in Terminal ─────▶ │  CLI commands        macdoc mcp (stdio)       │ ◀───── Claude Code /
 macdoc reclaim         │  reclaim, retire,    MCP tools: overview,     │        Claude Desktop
 macdoc vitals          │  explore, vitals,    opportunities, explain,  │        (launches `macdoc mcp`
 macdoc apply P-7F3K    │  scan, apply …       create_cleanup_plan      │         as a subprocess)
                        │       └──────┬──────────────┘                 │
                        │          ENGINE (probe, scan, recipes,        │
                        │          opportunities, planner)              │
                        │              │                                │
                        │       executor ◀── reachable ONLY from the    │
                        │          CLI (apply, dashboard remove,        │
                        │          quarantine), at a TTY, confirmed     │
                        └───────────────────────────────────────────────┘
```

- **The CLI is the product's own UI.** It works fully without Claude, and that's MVP B.
- **The MCP server is the same binary in a different mode** (`macdoc mcp`). Users never run it by hand. They register it once (`claude mcp add macdoc -- macdoc mcp`), and Claude Code or Claude Desktop launches it over stdio. The user's interface there is the Claude chat.
- **The two meet at the plan.** Claude calls `create_cleanup_plan` and tells the user to run `macdoc apply P-XXXX`. The user reviews the plan and confirms in Terminal. Claude can then read the audit log and report what happened.
- **Not planned:** embedding Claude inside the CLI (e.g. `macdoc ask`). It would need an API key and network access, which breaks the local-first principle. The MCP route gives the same chat experience through Claude apps the user already has.

## 2. Build order: safety first, convenience last

| Step | Phases | What we get | Can it delete? |
|---|---|---|---|
| 1 | 0 | Safety package (canonicalization, deny list, fuzz tests), sandbox mode, CI import guard | No |
| 2 | 1–4 | Read-only engine plus CLI: `doctor` and its sections (`health`, `space`, `wins`, `dev`, `projects`, `explain`, `scan`) and the `macdoc` dashboard. Used daily on the real Mac to tune recipes | No |
| 3 | 5 | Planner: sealed, expiring dry-run plans | No |
| 4 | 6 | Executor, quarantine and audit. Built and tested **only** inside `MACDOC_SANDBOX`. The first real apply is a SAFE-only plan the user approves | Yes (TTY only) |
| 5 | 7 | `slow`, `trend`, JSON schemas frozen → **v0.1 = MVP B** | Yes (TTY only) |
| 6 | 8–9 | MCP server: a thin layer over the engine reusing the frozen JSON, with no executor code. Plus handoff and hardening | No (MCP) |
| 7 | 10 | Optional daily read-only scans, the rest of the TUI picker (the dashboard already marks, plans and removes), slowness tips → **C done** | No new paths |

Rules for every phase:
- Small commits, with tests written alongside the code.
- `make test lint importcheck` must pass before moving on.
- No real cleanup runs on the developer's machine unless the user explicitly asks.

See [DEVELOPMENT_PLAN.md](DEVELOPMENT_PLAN.md) for tasks and acceptance criteria and [ORCHESTRATION.md](ORCHESTRATION.md) for how the work is split across agents.
