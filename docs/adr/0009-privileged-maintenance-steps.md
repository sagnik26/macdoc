# 0009: Privileged maintenance steps run only through the OS password prompt

**Status:** **Retired (2026-11-24).** Accepted on 2026-11-13; superseded by [ADR 0014](0014-reclaim-system-cleanup.md) for the rules that still apply to `sudo`, and by [ADR 0015](0015-no-refresh-plan-apply-mcp-plans.md), which removed the maintenance steps and the three-command allow-list this record describes. Kept for history; the code it names no longer exists.

## Context

Until now macdoc ran unprivileged and never used `sudo`; anything needing admin rights was advice for the person to run. A few useful maintenance steps (flushing the DNS cache, restarting `mDNSResponder`, `purge`) need root. The owner decided macdoc may run them, through the system's own password prompt, if the surface stays tiny and every earlier rule still holds.

## Decision

1. A recipe may set `privileged: true` (native method only) and `on_request: true`. The argv must exactly match `recipes.PrivilegedArgvAllowed`: `dscacheutil -flushcache`, `killall -HUP mDNSResponder`, `purge`. No shell, no wildcard, no placeholder. `qlmanage -r cache` and `qlmanage -r` need no rights and joined the ordinary native allow-list.
2. Only `internal/executor/privileged.go` runs `sudo`: `/usr/bin/sudo -- <tool>`, where the tool is a system path in `/usr/bin`, `/usr/sbin`, `/bin` or `/sbin`. `tools/forbidcheck` fails the build if any other file names `sudo` or a shell, including other files in `internal/executor`.
3. It refuses unless stdin and stdout are a terminal and the sandbox is off. `sudo` asks for the password itself: macdoc never reads, stores or logs it, never uses `sudo -S`, and runs `sudo -k` afterwards.
4. A privileged item is never in a plan Claude proposed: the planner refuses it (`ReasonPrivilegedFromClaude`) and the executor skips it too. The MCP server gets no new tool and cannot list `on_request` recipes. They are listed only by `macdoc doctor maintenance` and planned by ID.
5. Each privileged item needs its own y/N, like a RISKY one. `PlanItem.Privileged` is part of the sealed plan. The dry run prints "Needs administrator rights" and the exact argv. Audit records carry `privileged: true` and a `run` id.
6. An agent working in this repository must never run a privileged item or any `sudo` on the owner's machine.

## Consequences

- One new place can gain root, and it is small, typed and guarded by an allow-list, a build-time check and attack tests. Every other rule (IDs not paths, terminal approval, re-validation, protected paths, audit) is unchanged.
- A new privileged command needs an owner decision, an entry in `PrivilegedArgvAllowed`, and a test.
- The steps free no measurable space, so they show no size and are not in `doctor wins`.

## Not decided here

- Touch ID for `sudo` stays advice-only.
- Removing app bundles in `/Applications` is decided in ADR 0011 and does not use this privileged path.
- Repair commands such as `diskutil repairVolume` stay advice-only.
- A config whitelist to exclude individual maintenance tasks is not built.

Tests: the maintenance-step tests were deleted with the code (ADR 0015). The `sudo` guard that remains is `tools/forbidcheck/check/tests.TestSudoOnlyInThePrivilegedFile`, and the system-removal tests of ADR 0014 cover the rest.
