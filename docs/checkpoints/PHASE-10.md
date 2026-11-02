# Checkpoint: Phase 10 (Daily scan, picker, slowness tips) ✅ · built, awaiting user review

**Date:** 2026-11-02 · **Status:** built and tested. `make check` passes (race tests, lint 0 issues, importcheck, forbidcheck). Not pushed.

## Built
- **`macdoc schedule enable|disable`** (`internal/cli/schedule.go`, `internal/executor/schedule.go`). Enable writes `~/Library/LaunchAgents/com.macdoc.scan.plist` (`macdoc scan --quiet` at 09:00, low priority, output to the state folder). Disable removes that file, only if it's macdoc's. The person runs the printed `launchctl` line in Terminal.
- **`slow --tips`** (`internal/engine/slow_tips.go`). A tip only for a group whose memory `ps` measured, naming that size. Advice only.
- **Picker pre-marks SAFE suggestions** on each new scan (`preMarkSafe`). Advice-only items are never marked. Enter on an advice row always explains it, even when marks exist (this was a bug the change exposed, fixed and tested).

## Deviations
- **launchctl is not run by macdoc.** The plan says `schedule enable` installs the agent. Loading it runs a background job, and AGENTS invariant 2 keeps mutating commands in `internal/executor`. So enable writes the file and prints the load line, and disable removes the file and prints the unload line. Decided by the user on 2026-11-02.
- **Invariant 1 changed** for `schedule enable|disable` (AGENTS.md, SAFETY_MODEL §4.6a). Decided by the user on 2026-11-02.
- **Pre-marking SAFE items** means `p` plans the marked items, not the cursor row, when marks exist. That's the behaviour the plan asked for. It's worth a look at the dogfood week.

## Mutation checks
- Not run for Phase 10. The tests cover the foreign-file refusal, the remove-only-ours rule, the measured-only tips, the SAFE pre-mark, and the advice-row Enter.

## Open
- `schedule enable|disable --json` has frozen schemas (`schema/schedule-*.json`).
- The plist's 09:00 time isn't configurable yet.
- Not run on the real Mac: the `launchctl bootstrap` line. Run it yourself to check the agent loads.
