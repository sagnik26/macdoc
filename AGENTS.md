# AGENTS.md

Rules for AI coding agents working in this repository. People should read [CONTRIBUTING.md](CONTRIBUTING.md); this file is the short list of things an agent must not get wrong. macdoc deletes files on people's Macs, so a mistake here can lose someone's data.

## Never, on this machine

- Do not run `macdoc quarantine purge` or `restore`, or any native cleanup command (`brew cleanup`, `docker … prune`, `xcrun simctl delete`, `npm cache clean`, and the like), unless the user asks for it in this session.
- Read-only commands (`macdoc reclaim`, `retire`, `explore`, `vitals`, `scan`, and bare `macdoc`, all without a terminal) are fine to run for real.
- Executor tests run only under `MACDOC_SANDBOX=<tmpdir>` with fixture trees from `t.TempDir()`. Never point an executor test at a real home directory.
- Never answer `macdoc reclaim`'s prompts (the "What do you want to clean?" question, ticking its boxes, and the Clean button or Enter that starts the cleanup), and never feed them input or a pseudo-terminal. Agents run `reclaim` without a terminal, where it only lists; the answer and Enter there are the person's approval (ADR 0013).
- Do not run `macdoc reclaim … --yes` on this machine: it cleans with no prompt. The dashboard's `/reclaim` also starts cleaning SAFE and CAUTION items with no prompt once its scan ends (the owner's decision, 2026-11-24); never launch the dashboard and run it, or feed it keys or a pseudo-terminal. Do not run a system-location removal (a `system.*` recipe other than `system.ds_store`) either.
- Do not commit or push unless the user asks.

## The rules the code depends on

The reasoning is in [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md), [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md) and [docs/adr/](docs/adr/README.md). If a task seems to need breaking one, stop and ask.

1. Only `internal/executor` may delete, move or run mutating commands ([ADR 0003](docs/adr/0003-executor-is-the-only-mutator.md)). `internal/mcp`, `internal/engine` and `internal/planner` must never import it. Never loosen the import and forbidden-call guards.
2. Plans and tools take opportunity IDs, never paths ([ADR 0002](docs/adr/0002-ids-not-paths.md)). No command, flag or MCP tool may accept a path as a cleanup target.
3. Only a person at a real terminal approves a removal ([ADR 0001](docs/adr/0001-terminal-only-approval.md)). Never add `--yes` or any non-interactive bypass anywhere but `macdoc reclaim --tier safe|caution --yes` (SAFE and CAUTION quarantine moves only, [ADR 0014](docs/adr/0014-reclaim-system-cleanup.md) §5), and never let a plan Claude proposed skip per-item confirmation. The dashboard's `/reclaim` is the same kind of exception as `--tier safe|caution --yes`: it starts SAFE and CAUTION quarantine moves right after its own scan with no prompt (esc stops it between items), never RISKY, and is reachable only by a person typing it in the dashboard. The scan view only shows ([ADR 0018](docs/adr/0018-scan-is-view-only-chooser-deletes.md)): single items are deleted from `/clean` (dashboard) or `macdoc reclaim --choose` (terminal), where ticking a row and pressing Delete (or `y`) is the approval, RISKY included, with no typed code; `--choose` is never combined with `--yes` or `--dry-run`.
4. Every action comes from a recipe in `internal/recipes/catalog/*.yaml`. An item no recipe covers is advice-only. Native commands use a fixed argv from the recipe: no shell, no `--volumes`, no `docker system prune`. `sudo` appears only in `internal/executor/privileged.go`, never from a plan Claude proposed and never as `sudo -S`. An agent must never run a privileged item or any `sudo`. [ADR 0014](docs/adr/0014-reclaim-system-cleanup.md) (which took over the rules of the retired [ADR 0009](docs/adr/0009-privileged-maintenance-steps.md)) is the only way root is used: the `system` method for named system locations (`/Library/Caches`, logs, orphaned LaunchDaemons and helpers, interrupted Time Machine backups; `safety.CheckSystemTarget`): it runs only from `privileged.go`, with a command the executor builds, each with its own y/N, never from a Claude plan. An agent must never run one of those either.
   The one exception is the dashboard's Retire tab ([ADR 0017](docs/adr/0017-retire-deletes-by-program.md)): Enter on a program, then Delete or Cancel, deletes its app bundle and its attributed data folders through one built-in `retire.program` entry (`planner.RetireRecipe`) that allows exactly those paths. It is dashboard-only at a real terminal, takes no path from the person, never runs from a Claude plan, `reclaim` or `--yes`, and the protected paths and every re-check still apply.
   `reclaim` never lists an installed app's bundle (`apps.user_bundle`, `apps.system_bundle`); those are for `retire` and the scan view. Removing an app's leftover data is a recipe like any other ([ADR 0008](docs/adr/0008-app-leftovers-use-the-normal-removal-path.md)). Only `/Applications/<Name>.app` and Application Support leftovers are added, by RISKY recipes ([ADR 0011](docs/adr/0011-app-bundles-and-support-folders.md)). If the installed-app list can't be read, offer nothing.
5. Re-validate every item immediately before acting. On a mismatch, skip and audit; never "fix up" a target.
6. Protected paths are never touched (`docs/SAFETY_MODEL.md` §3). Configured protection can only add to the built-in list. The one exception is `/Applications/<Name>.app` one level down, through `safety.AppsPattern` and a RISKY quarantine recipe ([ADR 0011](docs/adr/0011-app-bundles-and-support-folders.md)); never widen `safety.AppsPattern`. The other exception is the `.DS_Store` sweep (`system.ds_store`, method `sweep`): exact file name only, CAUTION, never in a Claude plan ([ADR 0014](docs/adr/0014-reclaim-system-cleanup.md) §4).
7. Every number a person sees is measured, tool-reported, estimated or unknown. Never show unknown as zero.

## The MCP server

`macdoc mcp` lets Claude diagnose and **suggest**. It has no plan or apply tool ([ADR 0015](docs/adr/0015-no-refresh-plan-apply-mcp-plans.md)): the person cleans from `macdoc reclaim` or the dashboard. Do not add a tool that plans, executes, accepts a path, or runs a command. The tool list is pinned by `internal/mcp/tests.TestToolList`.

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
