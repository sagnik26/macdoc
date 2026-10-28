# AGENTS.md

Instructions for AI coding agents working in this repository. Read this before making changes.

## Project in one paragraph

macdoc is a safe, local-first macOS CLI (Go) that diagnoses why a developer Mac's storage is full and why it's slow. It proposes dry-run cleanup plans and applies them only after a human confirms in a terminal. A later phase adds a local MCP server so Claude can diagnose and *propose* plans, never execute them. See `README.md` for the what and why, and `docs/` for the full design.

## Current status

**In development.** Phases 0–5 are complete. Bare `macdoc` opens a Bubble Tea dashboard. `macdoc doctor` is the printed overview, with sections `health`, `space`, `wins`, `explain`, `dev`, `projects` and `scan`, all read-only. `macdoc plan` (and `p` in the dashboard) writes sealed dry-run plans into macdoc's state directory, and changes nothing else. Nothing can delete yet. See `docs/checkpoints/` for completed phases. Work proceeds phase by phase according to `docs/DEVELOPMENT_PLAN.md` §4. Don't skip ahead to later phases, and don't start a phase unless the user asks.

## Source of truth

| Topic | Document |
|---|---|
| What and why, locked decisions | `README.md` |
| Build order, how agents are orchestrated | `docs/BUILD_STRATEGY.md`, `docs/ORCHESTRATION.md` |
| Architecture, layout, phases, tests, risks | `docs/DEVELOPMENT_PLAN.md` |
| Safety rules (non-negotiable) | `docs/SAFETY_MODEL.md` |
| What can be cleaned and how | `docs/RECIPE_CATALOG.md` |
| Commands, output, tone, JSON contract | `docs/CLI_UX.md` |
| Claude/MCP integration (Phase C) | `docs/MCP_DESIGN.md` |

If code and docs disagree, stop and ask. Don't silently change either. When a design decision changes, update the relevant doc in the same change.

## Non-negotiable safety invariants

Never weaken these, even if a task seems to require it. Ask the user instead.

1. **Read-only by default.** Only `apply` and `quarantine restore|purge` may change anything outside macdoc's own state directory.
2. **Only `internal/executor` may delete, move or run mutating commands.** Calls to `os.Remove*`, `os.RemoveAll`, `os.Rename` (outside macdoc's state, plans and store), or mutating `exec.Command` calls anywhere else are bugs.
3. **`internal/mcp`, `internal/engine` and `internal/planner` must never import `internal/executor`.** A CI import guard enforces this. Never bypass or loosen it. The executor acts only on plans that pass `planner.Verify`.
4. **Plans contain opportunity IDs, never user-supplied paths.** No command, flag or MCP tool may accept a path as a cleanup target.
5. **Every action comes from a recipe** in `internal/recipes/catalog/*.yaml`. Unknown large items are advise-only.
6. **Apply requires a real TTY and typed confirmation.** Don't add `--yes` for CAUTION or RISKY items, and don't add non-interactive bypasses.
7. **Re-validate every item immediately before acting** (canonical path, fingerprint, allowlist, deny list, preconditions). On mismatch, skip and audit. Never "fix up" a target.
8. **No shell.** No `sh -c`, no string-built commands, no `sudo`. Native commands use fixed argv from recipes. Dynamic identifiers must match the tool's own listing.
9. **Never touch protected paths** (`docs/SAFETY_MODEL.md` §3), and never use `--volumes` or `docker system prune` in any recipe.
10. **Every user-visible number is measured, tool-reported, estimated or unknown.** Never show unknown as zero.

## Development rules

- **Language:** Go. Follow the package layout in `docs/DEVELOPMENT_PLAN.md` §3. Keep dependencies minimal: cobra, lipgloss, bubbletea + bubbles (the `macdoc` dashboard), `modernc.org/sqlite`, yaml.v3, and the official MCP Go SDK in Phase 8. Ask before adding others.
- **Never run real cleanups on the developer's machine while developing.** Executor tests run only under `MACDOC_SANDBOX=<tmpdir>` with fixture trees from `t.TempDir()`. Read-only commands (`doctor`, `scan`, `wins`, …) are fine to run for real.
- **Path-dependent code takes a root/home override** so it can be tested against fixtures.
- **External commands** go through the `platform.Runner` interface with timeouts and golden-file fakes in tests.
- **Low-RAM budget:** keep scanner peak RSS ≤ 64 MB and never hold the full file tree in memory. Benchmark scanner changes.
- **Output:** every command has a typed result struct and `--json` (versioned schema). Style human output only through `internal/cli/theme.go`. The plain theme must render exactly the uncolored text, so non-TTY output and tests stay stable. Follow the tone rules in `docs/CLI_UX.md` §3: neutral words, show restore cost, no scare language.
- **File names:** never end a Go file name in a GOOS or GOARCH suffix such as `_android.go`, `_ios.go`, `_linux.go`, `_windows.go` or `_arm64.go`. Go treats these as build constraints and silently excludes the file on macOS. This happened in P3-W1.
- **Recipes:** follow the checklist in `docs/RECIPE_CATALOG.md` §3. Each recipe needs a positive test, a look-alike negative test and a precondition-failure test.

## Commands

```
make build        # build ./bin/macdoc
make test         # go test ./... (race on)
make lint         # golangci-lint
make importcheck  # fails if mcp/engine import executor
make forbidcheck  # fails on delete/exec/shell calls outside the executor
make check        # test + lint + importcheck + forbidcheck
make fuzz         # fuzz safety/canonicalization
```

## Before finishing any change

- [ ] `make test lint importcheck` passes
- [ ] No new deletion, rename or exec calls outside `internal/executor`
- [ ] Docs are updated if behavior or design changed
- [ ] New recipes have all three tests
- [ ] JSON schema is updated and contract tests pass if output changed

## Git

Commit or push only when the user asks. Use small, focused commits with messages that explain why.
