# macdoc: product requirements

**Status:** v0.1.0 is released; the five-command restructure (below) is done on `main` and ships as v0.2.0.
**Owner:** sagnik26. **Licence:** MIT. **Last updated:** 2026-11-14.

## 1. Problem

A developer's Mac fills up and slows down for reasons the system hides. Finder shows an opaque "System Data" bucket.
Xcode, Android, Docker, Node, Python, Homebrew and local AI models quietly take tens of gigabytes. On an 8 GB Mac,
low RAM leads to heavy swap, swap grows on disk, free space shrinks, and builds start to fail. Existing cleaners
either explain nothing, delete blindly, or don't understand developer tooling, and manual cleanup risks deleting
something irreplaceable (Xcode Archives, Docker volumes).

## 2. Users

| User | Need |
|---|---|
| Full-stack or mobile developer on a space- or RAM-constrained Mac (primary) | Know where space and memory went, and get it back without losing work |
| Any Mac user who installs and uninstalls apps | Find leftovers and old installers safely |
| Someone using Claude or another agent to look after their Mac | Let the agent diagnose and propose, while a person stays in control of every removal |

## 3. Goals and non-goals

**Goals**
- Explain where storage and memory went, with honest sizes (measured, tool-reported, estimated or unknown).
- Suggest reclaimable space ranked by risk, each with its restore cost.
- Remove only what a person approves at a real Terminal, with a dry run first, a quarantine, an audit log and restore.
- Be simple to learn: reclaim, retire, explore, vitals and scan. macdoc cleans on its own (SAFE and CAUTION), asks first, or only suggests (ADR 0015).
- Stay local: no network calls from macdoc, no telemetry, no account.

**Non-goals**
- No background cleaning daemon, no automatic deletion, no "RAM boosting", no killing processes.
- No fake health numbers: the health score comes from fixed, shown rules.
- No copying of other tools' code or command names.
- No path-based cleanup: nothing accepts a path as a target.

## 4. Product shape

| Command | Job |
|---|---|
| `macdoc` | Interactive dashboard (overview summary when piped, `--json` or `--quiet`) |
| `macdoc reclaim [toolchain]` | What can be got back, ranked by safety, with IDs; at a terminal, you tick which risk types to clean and press Enter on the Clean button (typed answer: Enter alone is SAFE) (`--toolchains`, `--categories`, `--tier`, `--all`) |
| `macdoc retire` | Installed apps and the data that belongs to them |
| `macdoc explore [folder]` | Where the space is; `--growth` (what grew), `--projects` (idle projects and build folders) |
| `macdoc vitals` | Live CPU, memory, disk, network, battery and a 0 to 100 health score; `--memory`, `--watch` |
| `macdoc scan` | A fresh scan (no question, no reuse of the last one), then the full report |
| More | `log`, `quarantine`, `config`, `schedule`, `mcp`, `completion`, `version` |

The dashboard `/` menu mirrors this: `/scan /reclaim /retire /explore /vitals`; help and quit are in the footer (`?`, `q`, and typed `/help`, `/quit`).

## 5. Functional requirements

**F1. Diagnosis (read-only).** Disk, memory, swap, pressure, snapshots, top memory groups; "System Data" decoded;
growth between scans; projects with idle days, build folders and git state; installed software with attributed
data folders; live status; zombie and busy-process alerts (never ended).

**F2. Suggestions.** Every action comes from a recipe in `internal/recipes/catalog/*.yaml` (60 recipes). Each
suggestion has an ID, risk (SAFE, CAUTION, RISKY, ADVISE), size with its source, idle time and restore cost.
Anything no recipe covers is advice-only.

**F3. Reclaim.** At a real terminal `reclaim` lists everything by risk and asks "What do you want to clean?" (a row each for SAFE, CAUTION and RISKY; RISKY only when something is in it); the person ticks the risk types (Enter or space) and presses Enter on the separate Clean button, which is the only thing that starts the cleanup; with a typed answer, Enter alone cleans SAFE only. It covers what a deep clean covers without administrator rights (caches and logs of browsers, developer tools and desktop apps, sandboxed app caches, cached restore files, the Trash as a CAUTION item moved into quarantine first) and what removed apps left behind, but never an app that is still installed (that is `retire`); interrupted Time Machine backups are advice only. CAUTION and RISKY are cleaned only when named, RISKY keeps a per-item y/N and its typed ID, advice-only items have no row and can never be chosen, and an answer it does not understand cancels (not at a terminal, with `--json`, `--quiet` or `--dry-run` it only lists; ADR 0013). `apply` is for specific IDs, other tiers and sealed or Claude-proposed plans: it seals a plan (expires in 30 minutes), shows it, and applies it after the person types the
plan's code at a real Terminal. RISKY items, administrator steps and every item of a Claude-proposed plan are also
confirmed one by one. Each item is re-checked right before it is touched; a change means skip and record.

**F4. Removal paths.** The same paths serve `reclaim`'s chooser; RISKY items keep a per-item confirmation with the ID typed there, and administrator steps and Claude plans are never cleaned that way. Native tool commands from an exact allow-list, or a move into a private quarantine that is
then deleted through the same checks. `quarantine restore` brings back what is still there. Everything is in the
audit log (`macdoc log`), with a run ID per command.

**F5. Uninstall and leftovers.** Apps in `~/Applications` and `/Applications` (one exact exception, not Apple's
and not Mac App Store apps), leftover data of uninstalled apps in `~/Library` and Application Support, and
background items whose program is gone. Never offered when the installed-app list can't be read.

**F6. System cleanup with administrator rights.** Only the `system` method's fixed commands (ADR 0014), only from a plan a person made,
only through the system password prompt; macdoc never sees the password. (The maintenance steps and `refresh` were removed, ADR 0015.)

**F7. Claude (MCP).** `macdoc mcp` lets Claude read the diagnosis and suggest what to clean. It has no tool that plans, applies,
deletes, moves, restores or takes a path.

**F8. Output and automation.** Every command has `--json` with a frozen schema (`schema/`, `schema_version` 3);
`vitals --watch --json` streams NDJSON; settings can only make macdoc stricter (`config protect`,
`config hide` / `excluded_recipes`, timeouts through `MACDOC_TIMEOUT*`).

**F9. Distribution.** Homebrew tap (`brew install sagnik26/macdoc/macdoc`), a checksum-verified `install.sh`,
`llms.txt`, an agent skill, and Raycast and Alfred launchers.

## 6. Safety invariants (must never regress)

1. Only `internal/executor` deletes, moves or runs mutating commands; MCP, engine and planner never import it.
2. Plans and tools take IDs, never paths.
3. Only a person at a real terminal approves a removal; the exceptions are `reclaim --tier safe|caution --yes` and the dashboard's `/reclaim`, for SAFE and CAUTION quarantine moves (ADR 0014).
4. Native commands use a fixed argv from a recipe: no shell, no `docker system prune`; `sudo` appears only in
   `internal/executor/privileged.go`, only for system cleanup (ADR 0014), never from a Claude plan.
5. Re-check every item immediately before acting; never "fix up" a target.
6. Protected paths are never touched; configuration can only add protection. The one exception is an app bundle
   directly in `/Applications`.
7. Every number is measured, tool-reported, estimated or unknown; unknown is never shown as zero.

These are enforced by tests and by `make check` (import guard, forbidden-call guard, threat-model guard).

## 7. Success metrics

| Metric | Target |
|---|---|
| A new user finds what to do from `macdoc --help` alone | Seven headline commands, no more |
| Time from install to first useful answer | Under 2 minutes including the first scan |
| Unintended data loss from a macdoc action | Zero |
| Reclaimable space shown that the user could verify | Sizes match measured free-space change within the stated floor |
| Adoption signals | Installs, GitHub stars, and issues about unsafe behaviour (target: none) |

## 8. Delivery milestones

| # | Milestone | Status |
|---|---|---|
| 1 | Diagnosis, suggestions, sealed plans, apply, quarantine, audit log, MCP | done |
| 2 | Software discovery and attribution | done |
| 3 | Mole-gap work: installers, explorer, live status, alerts, uninstall and leftovers, maintenance, slimming report, run IDs, distribution | done |
| 4 | v0.1.0 release, Homebrew tap | done |
| 5 | Five-command restructure, merged `apply`, schema_version 3 | done (on `main`, uncommitted changes to commit) |
| 6 | v0.2.0 release: tag, publish, update the tap formula | pending owner go-ahead |
| 7 | Notarize the binary so Gatekeeper doesn't block it | pending |
| 8 | Submit to Homebrew core once there is a following | pending |

## 9. Risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| Breaking change to commands and JSON surprises existing scripts | Medium | v0.2.0 release notes; ADR 0012; schema_version bump |
| A recipe or the `/Applications` exception removes something wanted | Low | Exact allow-lists, RISKY per-item confirmation, quarantine and restore, attack tests |
| Sizes overstate gain on APFS clones | Medium | Sizes are a measured floor; stated in the README |
| Unnotarized binary is blocked by Gatekeeper | High until notarized | `xattr` note in install output and README |
| Dependency or Go standard-library vulnerability blocks a release | Medium | `govulncheck` gate in CI and release; keep Go current |

## 10. Open questions

- Should macdoc offer a signed and notarized installer package, and under which Apple developer account?
- Which of `reclaim --categories`, `--toolchains` and `explore --projects` should become dashboard tabs only?
- When to submit to Homebrew core (needs notability and a source build).

## 11. References

[README](../README.md) · [docs/ARCHITECTURE.md](ARCHITECTURE.md) · [docs/SAFETY_MODEL.md](SAFETY_MODEL.md) ·
[docs/THREAT_MODEL.md](THREAT_MODEL.md) · [docs/CLI_UX.md](CLI_UX.md) · [docs/adr/](adr/README.md) ·
[docs/RELEASING.md](RELEASING.md)
