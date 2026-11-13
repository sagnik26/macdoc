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

### 1. Installer files (Mole: `mo installer`) - done
Built on the existing `user.downloads_large_old` recipe rather than a new one: the minimum drops to 20 MB and the
idle time to 30 days, and a new `keep_files` recipe field plus a scanner `KeepFile` option make the scan keep
DMG, PKG, MPKG, ISO and XIP files in `~/Downloads` at any size (archives still need 100 MB, the scanner
threshold). Tests: the scanner option, `SplitKeepFile`/`KeepFileFunc`, the loader check, the detector, and a
scan-to-suggestion test (checked by disabling the wiring).
Correction to the first plan: `~/Desktop`, iCloud Drive (`Mobile Documents`) and `~/Library/Mail` are protected
paths (`docs/SAFETY_MODEL.md` §3), so no recipe may touch them; Mole cleans there, macdoc cannot. The Homebrew
download cache is already handled by the `brew.cleanup` recipe. So only Downloads gets a removal recipe. Reporting
installers in the protected places as advice-only is possible later.

### 2. Disk explorer (Mole: `mo analyze`) - done
`engine.Explore` reads the stored scan only (no new scan): a folder's kept children biggest first with their share,
a "smaller items" remainder so the entries add up, the biggest files below, and `complete`/`partial`/`unavailable`
(an unmeasured folder has no size; it is never shown as zero). `macdoc doctor explore [path] [--json]` and an
overlay opened with `enter` on the Space tab (`enter` opens a folder, `backspace` goes up, `esc` closes). It is
read-only and takes a path only to look at, never as a target (ADR 0002). Difference from the first plan: a separate
command `doctor explore` instead of `doctor space --path`, because `space` already has its own frozen JSON shape.

### 3. Live status (Mole: `mo status`) - done
`macdoc doctor status [--json] [--watch --interval D]` and a live block on the Health tab (refreshed every 3 s while
the tab is open, never on other tabs). Read-only collectors in `internal/probe/live.go`: CPU and load (`top`,
`sysctl`), network rates (`netstat -ib`, physical `en*` interfaces only so tunnels don't double-count), disk traffic
(`iostat`), battery and power (`pmset`, and `ioreg` for cycles, health and temperature, reading only named keys so the
battery serial is never read). `engine.Status` runs them in parallel; a failed probe leaves its section out with a
note. The health score is a fixed set of rules (`engine.HealthScore`). Not done, by choice: per-core CPU (no
read-only command gives it without sampling machinery), fan speed and sensor temperatures (`powermetrics` needs
root, which phase 3 does not use).

### 4. Zombie and CPU alerts (Mole: process alerts) - done
`doctor slow` and `doctor status` (text and `alerts` in `--json`) now list zombie parents (`ps` state Z, counted per
parent; a parent that is no longer listed is said so) and processes using 80% of a core or more, at most five of
each, read-only: macdoc only says who the parent is and that quitting it clears the zombies, and never signals
anything. One fixed `ps -axo pid=,ppid=,stat=,pcpu=,comm=` call (`probe.CollectProcessAlerts`). Difference from the
first plan: "sustained" is the recent average macOS reports in `ps`, not a window rebuilt from stored samples,
because the stored vitals hold memory and swap, not per-process CPU; building that would mean storing process lists,
which is more than an alert needs. A failed `ps` leaves the section out with a note, never "all clear".

### 5. Uninstaller and leftovers (Mole: `mo uninstall`, `mo clean` leftovers) - ADR 0008 - done within the safety rules
Done: Recipe `apps.leftovers` (detector `app_leftovers`, precondition `no_installed_app`): folders in
`~/Library/{Caches,Logs,Saved Application State,HTTPStorages,WebKit}` named after a bundle ID with no installed app or
LaunchAgent/LaunchDaemon owner, at least 1 MB, unchanged 60 days; CAUTION, quarantine; nothing is offered if the
installed-app list can't be read. Deviation: no confidence tiers (bundle-ID match only, never by name).
Also done: `apps.user_bundle` (an app in `~/Applications`, RISKY, own confirmation, precondition
`app_bundle_not_running`, Apple's apps skipped) and `apps.launch_agent_unload` + `apps.launch_agent_file` (a user
LaunchAgent whose program no longer exists: `launchctl remove <label>`, then the plist is quarantined; the argv is
`remove` not `bootout` because a native `{id}` can't contain the `/` that `gui/<uid>/<label>` needs).
Also done (ADR 0011, owner decision 2026-11-14): `apps.system_bundle` (`/Applications/<Name>.app`, RISKY, quarantine)
and `apps.support_leftovers` (Application Support, RISKY, 180 days). Only gap: root-owned bundles the system
refuses fail cleanly; there is no privileged removal. The plan below is the original scope.
- Opportunities generated from `doctor software` apps: one per app (bundle + its attributed data folders).
  Personal data (settings, plugins) is kept by default; the confirm step lets the person add data paths one by
  one; browser profiles and group containers are never preselected.
- Leftovers of uninstalled apps: Library entries named like a bundle ID with no installed app, no matching
  LaunchAgent, confidence tiers; risk CAUTION; quarantine. Name-only matches stay advisory.
- LaunchAgents/Daemons: list plists for the app; unload with a fixed `launchctl bootout` argv (`{id}` from the
  tool's own listing), then quarantine the plist.
- Preconditions: app not running (`app_not_running`), not under `/System`, not Apple-signed system apps, path
  under `/Applications` or `~/Applications`. Root-owned bundles fail cleanly (ADR 0011).
- Plans carry IDs, never paths (ADR 0002); re-validate every item right before acting.
Files: `internal/recipes/` (detector over `internal/inventory`), `internal/engine/uninstall.go`,
`internal/executor/` (reuse quarantine move), tests with fixture homes and fake `platform.Runner`.

### 6. System maintenance with privileged steps (Mole: `mo optimize`) - ADR 0009 - done
Done as below, with `doctor maintenance` and recipes `maintenance.*`. Not done: Touch ID (stays advice-only).
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
- Whitelist (done): `config` `excluded_recipes` can exclude individual tasks (add-only protection, like folders).
Docs to change: `AGENTS.md` rule 4, `CONTRIBUTING.md`, `docs/SAFETY_MODEL.md` §5, `docs/THREAT_MODEL.md`
(the "never uses sudo" statement), ADR index, forbidcheck tests.

### 7. Slim universal apps (Mole: `mo slim`) - done as advice (owner decision 2026-11-14)
The owner chose to keep slimming advice-only after the experiment (ADR 0010); 7b is not implemented. `launchctl remove` in phase 5 (instead of `bootout`) was also kept by the owner.
7a, read-only first: `lipo -archs` and `lipo -detailed_info` report each app's Intel slice and savings in
`doctor software`/Space. 7b, the action, only after an experiment under `docs/experiments/` shows a
copy-modify-resign flow keeps Developer-ID apps launching; then ADR 0010. If the experiment fails, 7b is
dropped and 7a stays as advice.
Status: 7a done (read-only advice in `doctor software`; `Slim` field, additive). 7b: experiment done (`docs/experiments/slim-universal-apps.md`); ad-hoc re-signing makes Gatekeeper reject Developer-ID apps and drops their Team ID, so 7b is dropped and slimming stays advice-only (ADR 0010).

### 8. Operations parity - done
`macdoc log --json` run IDs; tunable timeouts (`MACDOC_TIMEOUT_*` like Mole's, with sane defaults); shell
completion already exists; Touch ID for sudo stays advice-only (it edits `/etc/pam.d`); no self-update (macdoc
makes no network calls; Homebrew updates it).

### 9. Distribution and agent access - done (files only)
Added `install.sh`, `packaging/homebrew/macdoc.rb`, `llms.txt`, `skills/macdoc/`, `.claude-plugin/plugin.json`, `launchers/`. Not verifiable offline: install.sh against a real release, the Homebrew tap (owner must create it), shellcheck.
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
