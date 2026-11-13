# AGENTS.md

Rules for AI coding agents working in this repository. People should read [CONTRIBUTING.md](CONTRIBUTING.md); this file is the short list of things an agent must not get wrong. macdoc deletes files on people's Macs, so a mistake here can lose someone's data.

## Never, on this machine

- Do not run `macdoc apply`, `macdoc quarantine purge` or `restore`, or any native cleanup command (`brew cleanup`, `docker … prune`, `xcrun simctl delete`, `npm cache clean`, and the like), unless the user asks for it in this session.
- Read-only commands (`macdoc doctor …` including `doctor explore`, `doctor software`, `doctor status` and `doctor maintenance`, `scan`, `wins`, `slow`, `trend`) are fine to run for real. Listing maintenance steps is read-only; applying one is not.
- Executor tests run only under `MACDOC_SANDBOX=<tmpdir>` with fixture trees from `t.TempDir()`. Never point an executor test at a real home directory.
- Do not commit or push unless the user asks.

## The rules the code depends on

The reasoning is in [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md), [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) and [docs/adr/](docs/adr/README.md). If a task seems to need breaking one, stop and ask.

1. Only `internal/executor` may delete, move or run mutating commands ([ADR 0003](docs/adr/0003-executor-is-the-only-mutator.md)). `internal/mcp`, `internal/engine` and `internal/planner` must never import it. Never loosen the import and forbidden-call guards.
2. Plans and tools take opportunity IDs, never paths ([ADR 0002](docs/adr/0002-ids-not-paths.md)). No command, flag or MCP tool may accept a path as a cleanup target.
3. Only a person at a real terminal approves a removal ([ADR 0001](docs/adr/0001-terminal-only-approval.md)). Never add `--yes` or any non-interactive bypass, and never let a plan Claude proposed skip per-item confirmation.
4. Every action comes from a recipe in `internal/recipes/catalog/*.yaml`. An item no recipe covers is advice-only. Native commands use a fixed argv from the recipe: no shell, no `--volumes`, no `docker system prune`. `sudo` appears only in `internal/executor/privileged.go`, for the exact argvs in `recipes.PrivilegedArgvAllowed`, never from a plan Claude proposed and never as `sudo -S` ([ADR 0009](docs/adr/0009-privileged-maintenance-steps.md)). An agent must never run a privileged item (`macdoc apply` of one) or any `sudo`.
   Removing an app's leftover data is a recipe like any other ([ADR 0008](docs/adr/0008-app-leftovers-use-the-normal-removal-path.md)). Only `/Applications/<Name>.app` and Application Support leftovers are added, by RISKY recipes ([ADR 0011](docs/adr/0011-app-bundles-and-support-folders.md)). If the installed-app list can't be read, offer nothing.
5. Re-validate every item immediately before acting. On a mismatch, skip and audit; never "fix up" a target.
6. Protected paths are never touched (`docs/SAFETY_MODEL.md` §3). Configured protection can only add to the built-in list. The one exception is `/Applications/<Name>.app` one level down, through `safety.AppsPattern` and a RISKY quarantine recipe ([ADR 0011](docs/adr/0011-app-bundles-and-support-folders.md)); never widen `safety.AppsPattern`.
7. Every number a person sees is measured, tool-reported, estimated or unknown. Never show unknown as zero.

## The MCP server

`macdoc mcp` lets Claude diagnose and **propose** plans. Do not add a tool that executes a plan, accepts a path, or runs a command. The tool list is pinned by `internal/mcp/tests.TestToolList`, and the person applies plans in Terminal.

## Working here

- Go. Keep dependencies minimal and ask before adding one.
- Tests live in `internal/<pkg>/tests/` (`package tests`), not beside the code ([ADR 0006](docs/adr/0006-tests-in-a-tests-folder.md)). Plain `go test -cover` reports 0% under this layout; use `make cover`.
- Name each file with one word that says what it does, with no underscores or joined words (`apply.go`, not `apply_cmd.go` or `applycmd.go`). A test file is the same word plus `_test`. A name with an underscore can be read by Go as a build constraint and silently dropped (`_linux.go`, `_arm64.go`); a single word never is.
- Every path-dependent function takes a home or root override so tests can use fixtures. External commands go through `platform.Runner`.
- A new recipe needs a positive test, a look-alike negative test and a precondition-failure test (`docs/RECIPE_CATALOG.md` §3).
- A new exported name needs a doc comment that starts with its name. New code is held to the complexity limits in `.golangci.yml`.
- If code and a document disagree, stop and ask. Update the document in the same change when a design changes.

## Before finishing a change

`make check` passes (race tests, lint, import guard, forbidden-call guard, threat-model guard). If output changed, the JSON schema and its contract tests are updated: `go test ./internal/cli -run TestSchemasFrozen -update` is for additive changes only, and anything else bumps `schema_version`.
