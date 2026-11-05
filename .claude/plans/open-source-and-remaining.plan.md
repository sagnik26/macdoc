# Plan: Publish macdoc and finish what's left

**Source:** Conversation, 2026-11-05. Current `main` is `60a0165` (publication prep, still private).
**Complexity:** Medium overall. The publication steps are small, but two of them can't be undone once public.

## Summary
The code, tests, lint and safety checks pass. Publishing needs one decision about the git history, then a visibility change and cleanup of a stray remote branch. Phase 7 sign-off, the Phase 10 `launchctl` check and the Phase 9 follow-ups are yours to do later, as you said. Phase 11 items that aren't needed for a source release stay as follow-ups.

## Current state (verified 2026-11-05)
- `main` = `60a0165`, pushed. Repo visibility: **PRIVATE**.
- Remote branch `open-source-prep` exists by accident, at the same commit as `main`. It should go.
- Username `sagnikghosh` is still in history: commits `f0218eb` (Phase 1) and `a38b60c` (Phase 3), in fixture output. Current files are clean.
- Author email `sagnikjohnson26@gmail.com` is in all 115 commits. You chose to keep it.
- Done: `LICENSE` (MIT, `sagnik26`), `CONTRIBUTING.md`, `SECURITY.md`, `PRIVACY.md`, fixture paths replaced, tests/lint/importcheck/forbidcheck pass.

## Tasks

### A. Publish the source (needs your decision first)
1. **Decide the history.** Two options:
   - **Accept (recommended if the username isn't sensitive):** no history change. The username is already your GitHub handle.
   - **Rewrite:** `git filter-repo` to replace the path in all commits. Every commit id changes, so it needs a force-push. Anyone with a clone must re-clone. Back up first with `git clone --mirror`.
   Validate for either: `git log --all -S sagnikghosh` shows the decided state.
2. **Delete the stray remote branch** `open-source-prep`: `git push origin --delete open-source-prep`. Validate: `git ls-remote --heads origin` lists only `main`.
3. **Make the repo public:** `gh repo edit --visibility public --accept-visibility-change-consequences`. Validate: `gh repo view --json visibility` shows `PUBLIC`.
4. **Add the repo description and topics** on GitHub (macOS, disk cleanup, developer tools) so it's findable. Optional.

Risk: publishing exposes the email and any history you chose to keep. Once public, a copy may be cached even if deleted. Confirm step 1 before step 3.

### B. Phase 7 sign-off (you, later)
5. Decide whether the 10 GB reclaim target is met or re-scoped. Currently 0.88 GB, with no applies since 2026-10-28. Then record the result in `AGENTS.md`, `README.md` and `docs/checkpoints/PHASE-7.md`.

### C. Phase 10 check (you, later)
6. Run the `launchctl bootstrap` line printed by `macdoc schedule enable` in Terminal. Confirm the agent loads, then record it in `docs/checkpoints/PHASE-10.md`.
7. Make the plist's 09:00 time configurable (optional follow-up).

### D. Phase 9 follow-ups (later)
8. Add item limits to `list_projects`, the storage breakdown and growth history.
9. Add a time limit to `refresh_scan`.
10. Record the first real Claude plan once you've made one.

### E. Phase 11 items not needed for source release (later)
11. Generic thresholds: replace the hard-coded numbers (for example the 100 MiB scan floor in `internal/scanner/scanner.go:87` and `internal/engine/trend.go:68`) with settings or measured values. Needs a scope decision.
12. Homebrew tap. Needs the release plan.
13. Signing and notarization. Needs an Apple developer account. Without it, downloaded binaries trigger Gatekeeper warnings.

### F. Phase 7 follow-ups (later)
14. JSON command names: decide whether `wins`, `explain` and `storage` stay as doctor sections or become top-level names. Changing them is a contract change, so it needs a schema version decision.

## Validation (every step that changes the repo)
```bash
make test
make lint
make importcheck
make forbidcheck
```
Publishing steps also check `gh repo view` and `git ls-remote`.

## Risks
| Risk | Likelihood | Mitigation |
|---|---|---|
| Publishing exposes history you didn't mean to share | Medium | Decide step 1 first; back up before any rewrite |
| A history rewrite breaks other clones | High if chosen | Only with your go-ahead; document re-cloning |
| Deleting the remote branch loses unmerged work | Low | It points at `main`'s commit; confirm with `git log main..origin/open-source-prep` first |
| Phase 9/10/11 items start without a scope | Medium | Each one waits for your decision |

## Execution status (recorded 2026-11-05)
- [x] Step 1, history: **accepted as is** (user's decision). No commit ids changed.
- [x] Step 2, stray remote branch `open-source-prep`: deleted. `origin` lists only `main`.
- [ ] Step 3, make the repo public: **handed to the user** ("I will do it myself"). Repo is still PRIVATE.
- [ ] Step 4, GitHub description and topics: optional, left to the user with step 3.
- Steps 5–14 (Phase 7, 9, 10, 11 items): left to the user or to later scope decisions, as stated above.

## Acceptance
- [ ] History decision recorded (step 1)
- [ ] Remote branch removed; only `main` on `origin`
- [ ] Repo visibility is `PUBLIC`, if you chose to publish
- [ ] Phase 7 result, Phase 10 load check and Phase 9 items recorded by you
- [ ] Phase 11 items only after your scope decisions

## Questions for you
1. Accept the username in history, or rewrite it?
2. Publish now after step 1, or wait until the Phase 7 and Phase 10 checks are done?

**WAITING FOR CONFIRMATION** before any step changes the repo.
