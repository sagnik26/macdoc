# 0012: Five commands and one apply

**Status:** Accepted (owner decision, 2026-11-14); **partly superseded by [ADR 0015](0015-no-refresh-plan-apply-mcp-plans.md)**: `refresh` and `apply` no longer exist. Supersedes only the command names used in earlier records; it changes no safety rule. **Recorded in:** `internal/cli`, `schema/`, `docs/CLI_UX.md`, `README.md`.

## Context

The CLI had grown a `doctor` command with a dozen sections (`wins`, `dev`, `space`, `software`, `maintenance`, `explore`, `trend`, `projects`, `status`, `health`, `slow`, `scan`) and a separate `plan` command beside `apply`. Several sections answered nearly the same question, and sealing a plan and applying it were two steps people had to connect by copying a plan ID.

## Decision

1. The read-only surface is bare `macdoc` (the overview) plus five commands that each answer one question, and `scan`:
   - `reclaim [toolchain] [--all] [--tier T] [--toolchains] [--categories]`: what space can I get back (was `doctor wins`, `dev`, `space`).
   - `retire`: what software and leftovers can I remove (was `doctor software`).
   - `refresh`: what maintenance can I run (was `doctor maintenance`).
   - `explore [path]`, `explore --growth [--days N]`, `explore --projects [--idle N]`: where the space went (was `doctor explore`, `trend`, `projects`).
   - `vitals [--watch --interval D]`, `vitals --memory [--days N] [--tips]`: how the Mac is doing now and whether memory is slowing it (was `doctor status`, `health`, `slow`).
2. `apply` is the only command that plans and removes. `macdoc apply <O-ids>|--safe|--tier T|--recipe R` seals a plan, shows the dry run, asks the same confirmations at a real Terminal and applies. `--dry-run` only seals and shows the plan (what `plan` did). `macdoc apply P-7F3K` applies an existing sealed plan, including one Claude proposed. `--list` and `--show P-7F3K` replace `plan list` and `plan show`. With no arguments it applies nothing and prints pending plans and top suggestions.
3. The safety rules are unchanged: a real Terminal only, no `--yes`, the typed plan code, a per-item confirmation for RISKY, privileged and Claude-proposed items, IDs never paths, re-validation before each action, and the executor as the only mutator (ADRs 0001 to 0003).
4. `doctor` and `plan` are removed with no aliases. A script that uses an old name fails loudly instead of doing something unintended.
5. The JSON output contract changes with the names, so `schema_version` becomes `"3"` and the files in `schema/` are renamed to match (`vitals.json`, `explore.json`, `retire.json`, `refresh.json`, `reclaim.json`, `scan.json`, `apply.json`, `apply-show.json`, `apply-list.json`).
6. The dashboard `/` menu becomes `/overview /scan /vitals /explore /retire /refresh /help /quit`. Typed-only `/plan` and `/apply` still work and map to the apply flow.
7. Accepted ADRs keep the command names they were written with. This record supersedes the names only: read `doctor wins` as `reclaim`, `doctor software` as `retire`, `doctor maintenance` as `refresh`, and `plan` as `apply --dry-run`.

## Consequences

- Fewer overlapping commands and one fewer step between a suggestion and its removal.
- Every script, launcher, skill and document that used the old names had to change; there is no compatibility path.
- The MCP tools and their output are unchanged, except that returned commands read `macdoc apply P-...`.
- Agents may run the read-only commands freely, and `macdoc apply` only with `--dry-run` when the user asks (`AGENTS.md`).

## Not decided here

- Whether `refresh` will gain a way to run steps outside `apply`. Applying a maintenance step still goes through a plan.
