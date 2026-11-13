# Close the Mole gaps in macdoc

## Context
Mole (github.com/tw93/mole, GPL-3.0, shell + Go) is the reference cleaner. macdoc already matches or beats it on
safety (sealed plans, terminal-only approval, quarantine + restore, audit log, MCP propose-only), project
artifacts, trend, and the new software inventory. Mole still does more in these areas: installer files, a
browsable disk explorer, a live status dashboard with zombie/CPU alerts, an app uninstaller with leftovers,
system maintenance, slimming universal apps, and distribution (brew, scripts, agent skill, launchers). The goal
is every Mole capability in macdoc, plus macdoc's own extras, without weakening the safety rules except where
the user chose to.

Decisions made with the user:
- Commit the current work to a new branch `software-discovery` as split commits, then push (step 0).
- App uninstall and leftovers go through the normal macdoc path: sealed plan, terminal approval, per-item
  confirm, quarantine then delete, audit log, restore. New ADR 0008.
- Privileged maintenance steps are allowed through the OS password prompt. This changes AGENTS rule 4 ("no sudo")
  and the threat model, so it gets ADR 0009 and the bounds below.
- Do not copy Mole's code (GPL-3.0); copy behaviour only.

## Step 0: commit and push (first action after approval)
Branch from the current tree: `git switch -c software-discovery`. Commits, in order, staged by path:
1. `chore: remove the engineering plan, phase checkpoints and task records` (the 43 deleted files).
2. `feat: discover installed software` (`internal/inventory`, store migration 7, `engine/software.go`, recipe
   `match:` and the catalog, `schema/*`, ADR 0007, their tests and docs).
3. `feat(cli): /installed view, overview summary, palette and dead-code cleanup` (everything else).
Check `go build ./... && make check` after the last commit (earlier ones may not build alone where a file has
mixed changes; if so, squash them forward). Then `git push -u origin software-discovery`. No force push.
The unstaged/`A` mix in `git status` is normalised first with `git add -A` per commit scope.

## Phases (read-only first, removal last)
Each phase ends with `make check`, a read-only run on this Mac, and doc updates. Tests live in
`internal/<pkg>/tests/`; every path-dependent function takes a home/root override; executor tests only under
`MACDOC_SANDBOX`. Never run `apply` for real.

### 1. Installer files (Mole: `mo installer`)
New recipes for DMG, PKG, MPKG, ISO, XIP in Downloads, Desktop, `~/Library/Caches/Homebrew/downloads`, iCloud
Downloads, Mail Downloads. Detector `installer_files` over scan nodes; method quarantine; risk CAUTION; restore
"re-download". ZIPs only when the name or contents say installer (else advisory). Files: `internal/recipes/`
(new detector, `catalog/system.yaml` or new `installers.yaml`), tests (positive, look-alike `.dmg.txt`,
precondition). The scanner must keep these file nodes (`keep_names`/extension hook).

### 2. Disk explorer (Mole: `mo analyze`)
Read-only tree built from stored scan nodes, no new scan. `engine.Explore(scanID, path)` returns children with
bytes, share, scan status (complete/partial/unknown, never zero for unknown), plus the largest files. UI: a
block in the Space tab with `enter` to descend, `backspace` to go up, no deletion (ADR 0002: no paths as
targets), and `macdoc doctor space --path <dir> --json`. Files: `internal/engine/explore.go`,
`internal/cli/tables.go` (reuse `Grid`/`block`), `internal/store` query reuse (`Nodes` with Prefix/MaxDepth).

### 3. Live status (Mole: `mo status`)
`macdoc doctor status [--json] [--watch --interval 2s]` and a live block in the Health tab. Read-only probes in
`internal/probe`: CPU per core and load, memory pressure, disk I/O, network rates, battery and power, thermals
(no `powermetrics`, it needs root), top processes, a health score. Fixed read-only commands through
`platform.Runner` (`ps`, `vm_stat`, `iostat`, `netstat`, `pmset`). Frozen schema `doctor-status.json`. Watch mode
streams NDJSON.

### 4. Zombie and CPU alerts (Mole: process alerts)
Add to `slow`/`status`: zombie parents (`ps` state Z) and sustained CPU over a window, read-only, never
terminates anything. Window sampling reuses the vitals samples in the store.

### 5. Uninstaller and leftovers (Mole: `mo uninstall`, `mo clean` leftovers) - ADR 0008
- Opportunities generated from `doctor software` apps: one per app (bundle + its attributed data folders).
  Personal data (settings, plugins) is kept by default; the confirm step lets the person add data paths one by
  one; browser profiles and group containers are never preselected.
- Leftovers of uninstalled apps: Library entries named like a bundle ID with no installed app, no matching
  LaunchAgent, confidence tiers; risk CAUTION; quarantine. Name-only matches stay advisory.
- LaunchAgents/Daemons: list plists for the app; unload with a fixed `launchctl bootout` argv (`{id}` from the
  tool's own listing), then quarantine the plist.
- Preconditions: app not running (`app_not_running`), not under `/System`, not Apple-signed system apps, path
  under `/Applications` or `~/Applications`. Root-owned bundles need the privileged path (phase 6).
- Plans carry IDs, never paths (ADR 0002); re-validate every item right before acting.
Files: `internal/recipes/` (detector over `internal/inventory`), `internal/engine/uninstall.go`,
`internal/executor/` (reuse quarantine move), tests with fixture homes and fake `platform.Runner`.

### 6. System maintenance with privileged steps (Mole: `mo optimize`) - ADR 0009
Bounds, all enforced in code and tests:
- Only `internal/executor` may run privileged steps, through one typed function (new
  `executor/privileged.go`); `tools/forbidcheck` allows `sudo` only in that file.
- Exact argv allow-list in the catalog (`PrivilegedArgvAllowed`): DNS flush (`dscacheutil -flushcache`,
  `killall -HUP mDNSResponder`), memory `purge`, plus unprivileged `qlmanage -r cache` and `qlmanage -r`.
  No shell, no wildcard, no `rm` under sudo, no repair commands (`diskutil repairVolume` stays advice-only).
- Needs a real TTY (ADR 0001), a typed confirmation, and never from a plan Claude proposed
  (`planner` refuses privileged items when the origin is Claude; MCP gets no new tool).
- The OS shows the password prompt; macdoc never reads, stores or logs it, never uses `sudo -S`, and runs
  `sudo -k` afterwards. The dry run shows the exact command; the audit log records the privileged step.
- Whitelist: `config` can exclude individual tasks (add-only protection, like folders).
Docs to change: `AGENTS.md` rule 4, `CONTRIBUTING.md`, `docs/SAFETY_MODEL.md` §5, `docs/THREAT_MODEL.md`
(the "never uses sudo" statement), ADR index, forbidcheck tests.

### 7. Slim universal apps (Mole: `mo slim`)
7a, read-only first: `lipo -archs` and `lipo -detailed_info` report each app's Intel slice and savings in
`doctor software`/Space. 7b, the action, only after an experiment under `docs/experiments/` shows a
copy-modify-resign flow keeps Developer-ID apps launching; then ADR 0010. If the experiment fails, 7b is
dropped and 7a stays as advice.

### 8. Operations parity
`macdoc log --json` run IDs; tunable timeouts (`MACDOC_TIMEOUT_*` like Mole's, with sane defaults); shell
completion already exists; Touch ID for sudo stays advice-only (it edits `/etc/pam.d`); no self-update (macdoc
makes no network calls; Homebrew updates it).

### 9. Distribution and agent access
Homebrew formula/tap, `install.sh` (checksum-verified from the release workflow), `llms.txt`, an agent skill and
plugin manifest for Claude Code/Codex/Cursor that always runs `plan` before anything, Raycast/Alfred launcher
script, multilingual README later. Extends `docs/RELEASING.md` and the existing goreleaser workflow.

## Beyond Mole (macdoc differentiators to keep and grow)
Sealed plans with expiry, quarantine and restore, per-item confirm, MCP propose-only, software inventory and
attribution, trend and growth, project git-state risk, scheduled read-only scans.

## Risks
- Phase 6 widens the attack surface: keep the allow-list tiny, add attack tests (planted `sudo`, `sh -c`,
  Claude-origin privileged item, non-TTY) before any real run.
- Phase 5 removes app bundles: fixtures only in tests; the real Mac gets read-only runs.
- Phase 7b may break app signatures: gated by the experiment.
- Scope is large: ship phase by phase, each its own branch and PR.

## Verification
Per phase: unit and fixture tests (positive, look-alike, precondition), `make check`, a read-only run of the new
command on this Mac, docs and frozen schemas updated (additive only unless `schema_version` bumps).
End to end for phases 5 and 6: sandbox plan and apply tests under `MACDOC_SANDBOX`, attack tests for the
privileged path, and a manual dry run in a terminal; no real uninstall or sudo run without the user.
