# Checkpoint: Phase 5 (Planner → `macdoc plan`) ✅ · USER REVIEW BEFORE PHASE 6

**Date:** 2026-10-28 · **main:** see `git log` · **Status:** complete. Phase 6 (executor, quarantine, audit) is the first phase that can delete. The orchestration plan requires a user review before any real apply, and the executor tests are written first, from SAFETY_MODEL.md.

## Built
- **`internal/planner`** (new; must not import the executor, and importcheck now asserts that):
  - `ParseIDs` accepts only opportunity IDs. Anything path-like is refused ("never paths").
  - `Build` works from the latest complete scan (≤ 6 h old). It selects by IDs, tier or recipe, and refuses advice-only, blocked and unknown IDs with reasons. It re-checks every target *now*: a quarantine target must still exist, not be a symlink, keep its canonical path and pass `CheckTarget`; a native command must exactly match its recipe and its tool must be installed. It fingerprints each folder (dev, ino, mtime_ns, alloc). Items are sorted by tier, then size.
  - `Save` gives the plan an unused `P-XXXX` ID (no look-alike characters), seals it with SHA-256, writes the file create-only with mode 0600, then records the hash in the store (`plans` table, migration v2).
  - `Load` refuses unknown fields and trailing data, and accepts only well-formed IDs.
  - `Verify` checks the seal, the store copy, scan, expiry and applied status, then re-checks every item against the catalog and the safety policy. A plan whose file and store row are both forged still can't carry a command or path the planner wouldn't make.
  - `Actions` is the one "item → action" translation, used by both the dry-run view and the `Performer` interface the Phase 6 executor will implement. `Perform` keeps going after a failed item.
- **Engine:** `CreatePlan` (reuses the dashboard's opportunities when they're from the latest scan), `ShowPlan` (status: pending, expired, applied or invalid) and `ListPlans`. The snapshot caveat comes from `tmutil listlocalsnapshots`.
- **CLI:** `macdoc plan <O-id…> | --safe | --tier … | --recipe …`, `plan show <P-id>`, `plan list`, all with `--json`. The `wins` and `dev` footers point to `macdoc plan`.
- **Dashboard:**
  - On Suggestions, `space` marks items (✓). Advice items refuse to be marked.
  - The header shows "✓ N marked · size · p plan".
  - `p` seals a plan from the marked items, or the selected one. `/plan safe|caution|risky` plans a whole tier.
  - The dry run opens as an overlay, and the help and safety text explain what a plan is.

## Real Mac
- `macdoc plan --safe` gave P-ZDHP: 4 items, 825 MB (Homebrew `brew cleanup --prune=all`, `uv cache prune`, and two log folders to quarantine).
- In the dashboard, marking two items and pressing `p` gave P-8MGH in under 4 s.
- Editing an item's argv in the plan file made `plan show` report it as invalid. Restoring the original bytes made it verify again.
- A path argument, `--tier advise` and an unknown ID were all refused with reasons.
- No cleanup command ran. Nothing outside macdoc's state folder changed.

## Done-when
| Criterion | Test |
|---|---|
| A hand-edited plan file is rejected | `TestEditedPlanIsRejected`: command, target, risk, expiry, an added item, an unknown field, a removed seal, scan, size. Plus `TestForgedPlans` (re-sealed file, forged file and row) and `TestLoadRejectsTrailingData` |
| An expired plan is rejected | `TestSaveLoadVerify`, `TestCreateAndShowPlan` |
| The dry run matches exactly what the executor would do | `TestDryRunParity`: a recording `Performer` receives the same actions, in the same order, as the dry run's `→` lines, and nothing else |

Also tested:
- path and malformed IDs refused (`TestParseIDs`, `TestPlanSelector`)
- target re-checks: gone, symlink, protected, edited command, tool missing, unknown recipe
- the stale-scan refusal, create-only writes, ID alphabet and uniqueness
- store plan records and their schema constraint
- the dashboard's mark/unmark, advice refusal, `p` with and without marks, `/plan safe`, refusal messages and the overlay

## Not done (by design or later)
- The `plan_created` audit event waits for the audit log (Phase 6).
- Plan expiry is fixed at 30 minutes; there's no config file yet.
- Old plan files aren't cleaned up. They're tiny, and the purge belongs with Phase 6's state management.

## Try it
```
macdoc plan --safe
macdoc plan show <P-id>
macdoc plan list
macdoc            # → 2 Suggestions → space to mark → p
```
