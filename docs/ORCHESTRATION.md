# macdoc: Orchestration (how the build is run)

The build is run by a **tech-lead Claude session** (the orchestrator), which delegates bounded tasks to **worker Claude sessions** running in visible tmux panes, then verifies and merges their work.

## 1. Decisions (2026-10-26)

| Topic | Decision |
|---|---|
| Visibility | Every worker runs as its own **headless** Claude Code session (`claude -p`, stream rendered live) in a **tmux pane** the user can watch |
| Concurrency | **1 implementer at a time**, plus 1 reviewer only when needed. The dev Mac has 8 GB of RAM and was already using ~2.8 GB of swap at baseline |
| Permissions | **Narrow allowlist passed as CLI flags** to each headless worker; anything else is refused automatically. Never "skip all permissions". (Interactive workers were dropped: the tech lead may not drive another Claude session via tmux keystrokes, and the user chose headless mode.) |
| Checkpoints | Pause for user review **after Phase 0, after Phase 3, after Phase 6 (before any real apply), at v0.1 (Phase 7), and after Phase 9** |
| Tooling | Go 1.27.3 (auto toolchain via `GOTOOLCHAIN=auto`), tmux 3.8, golangci-lint 2.15, jq, git, Claude Code CLI |

## 2. Roles

**Tech lead (orchestrator session)**
- Owns the **contracts**: core types (`Opportunity`, `Plan`, `Recipe`, result structs), the `platform.Runner` interface, package boundaries, JSON schemas and `go.mod` dependencies. These are written *before* work is delegated.
- Writes a task brief per worker (`docs/tasks/<phase>-<id>.md`).
- Launches workers, monitors their panes, verifies, merges into `main`, commits and keeps the docs current.
- Reports to the user at each checkpoint.

**Implementer (W):** one well-bounded package per task, with tests. It works only inside its worktree and on its own branch.

**Reviewer (R):** a fresh session that didn't write the code. It tries to break safety-critical code and adds failing tests for what it finds.

## 3. Task split

| Phase | Tech lead | Implementer tasks (run one after another) | Reviewer |
|---|---|---|---|
| **0 Foundations** | Go module, layout, core types, Runner, Makefile | **P0-W1** `internal/safety` (canonicalize, deny list, fuzz) · **P0-W2** CI, lint config, import guard, fixture builder, sandbox mode | **P0-R1** attack `safety` |
| **1 Probe → doctor** | `doctor` wiring | **P1-W1** hardware/memory/swap parsers (golden tests) · **P1-W2** disk/APFS/snapshot parsers plus cgo shim | none |
| **2 Scanner + store** | Integration, RSS benchmark sign-off | **P2-W1** crawler (streaming, hard links, QoS, cancel) · **P2-W2** SQLite store and migrations | none |
| **3 Recipes** | Recipe YAML schema, loader | **P3-W1** Xcode/Apple + Android recipes · **P3-W2** Node/RN/Brew + Docker/Python/AI/System recipes · **P3-W3** classifier, ranking, `wins`/`storage`/`dev`/`explain` | **P3-R1** recipe lint: nothing reaches protected paths |
| **4 Projects** | none | **P4-W1** project discovery and staleness | none |
| **5 Planner** | Plan format and sealing spec | **P5-W1** planner and dry-run output | none |
| **6 Executor** ⚠ | Write the executor tests first, from SAFETY_MODEL.md | **P6-W1** executor, quarantine, audit (sandbox only) | **P6-R1** TOCTOU, symlink swap, crash, tampering |
| **7 Polish** | Release v0.1 | **P7-W1** `slow` and `trend` · **P7-W2** JSON schemas and contract tests | none |
| **8–9 MCP** | Tool list | **P8-W1** MCP server · **P9-W1** handoff and hardening | **P9-R1** prompt injection, executor unreachable |
| **10 Extras** | none | **P10-W1** launchd scan · **P10-W2** TUI picker · **P10-W3** slow tips | none |

> **As run (2026-11-02):** Phases 0–3 ran as above, with briefs and reports in `docs/tasks/`. From Phase 4 on, the tech-lead session built each phase itself, verified it with the same gates (§5) and recorded it in `docs/checkpoints/PHASE-N.md`. No worker or reviewer sessions were started, so there are no task files for P4-W1 to P8-W1 or P6-R1. Phase 9 was also built by the tech-lead session (2026-11-02) with an inline adversarial review; the separate P9-R1 red-team session hasn't run.

## 4. Mechanics

### Worktrees and branches
- Each task gets its own git worktree at `macdoc/.worktrees/<task-id>` on branch `task/<task-id>`. `.worktrees/` is git-ignored.
- Workers commit on their own branch only. The tech lead merges into `main`.

### Task brief (`docs/tasks/<task-id>.md`)
Each brief contains:
- the goal and the relevant doc sections to read
- **allowed files and packages** (everything else is off-limits)
- the contracts to use (types and interfaces it may not change)
- required tests and fixtures
- the definition of done
- the reporting format: the worker writes `.task/REPORT.md` in its worktree (what was done, test output, open questions) and commits

### Worker runtime: `scripts/orchestration/run-worker.sh`
- Workers run headless: `claude -p "<prompt>" --output-format stream-json --verbose --permission-mode acceptEdits` with explicit `--allowedTools` (Read/Glob/Grep — Edit/Write are deliberately not allowlisted so `acceptEdits` limits writes to the worktree, `go`, `gofmt`, `make`, `golangci-lint`, `git status|diff|add|commit|log|show`, `mkdir`, `ls`) and `--disallowedTools` (`git push|checkout|merge|worktree|reset`, `rm`, `sudo`, `curl`, `wget`, WebFetch, WebSearch, Agent).
- The JSON stream is saved to `.task/stream.jsonl` and rendered in the pane by `scripts/orchestration/format-stream.jq` (messages, tool calls, errors, and a final summary line).
- Workers can't ask questions mid-task. They record questions in `.task/REPORT.md`.
- `run-worker.sh` starts `caffeinate -i -w <its PID>`, which stops the Mac from idle-sleeping while a worker runs and ends with it. A manual sleep or closing the lid still works. Added after workers stalled for hours while the Mac slept.
- Workers must run everything in the **foreground**. In headless mode, ending a turn ends the session, so a worker that backgrounds a job and "waits" just stops (this happened in P2-W1). `run-worker.sh` appends this rule to every prompt.
- **Feedback loop:** the tech lead resumes the same session with `run-worker.sh <task> --continue "<review feedback>"`. Nobody types into a worker pane.

### tmux layout (`tmux attach -t macdoc-build`)
```
┌──────────────────────────────┬──────────────────────────────┐
│ WORKER · current task        │ REVIEWER (only when active)  │
│ headless claude (stream)     │ headless claude (stream)     │
├──────────────────────────────┴──────────────────────────────┤
│ STATUS · task branches, last commits, make test, mem/swap    │
└─────────────────────────────────────────────────────────────┘
The tech-lead session stays in the user's main window.
```
The status pane refreshes periodically: active branches, latest commit per branch, last `make test` result, memory pressure and swap. Before starting a worker, the tech lead checks memory pressure. If it's high, the tech lead waits or asks the user to close heavy apps.

## 5. Verification protocol (every task)

1. **Scope:** the diff touches only the allowed files from the brief.
2. **Gates:** the tech lead runs `make test lint importcheck` itself on the task branch. A worker's claim of passing isn't enough.
3. **Safety scan:** no new `os.Remove*`, `os.Rename`, mutating `exec.Command`, `sh -c` or `sudo` outside `internal/executor`.
4. **Contracts:** shared types and interfaces are unchanged. Interface changes go through the tech lead.
5. **Review:** the tech lead reads the code against the docs and either merges or sends specific feedback to the same pane.
6. **Safety-critical tasks** (safety, recipes lint, executor, MCP) also need a clean reviewer pass. Reviewer findings become tests before merge.
7. **Archive** the worker's `.task/REPORT.md` (plus the review decisions) to `docs/tasks/reports/<task-id>.md`.
8. **Merge** into `main` (no fast-forward, so each task is one visible merge), then remove the worktree and task branch.

## 6. Checkpoint reports to the user

At each checkpoint the tech lead gives:
- what was built
- test and coverage summary
- reviewer findings and fixes
- commands to try on the real Mac (read-only unless stated)
- known issues
- what's next

The next phase starts only after the user approves.
