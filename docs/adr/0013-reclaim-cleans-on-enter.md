# 0013: `reclaim` cleans on a single Enter

**Status:** Accepted (owner decision, 2026-11-16; amended 2026-11-16 to add a risk-type chooser). Relaxes, for SAFE items by default, the typed plan code that ADR 0012 kept for `apply`; it changes no other safety rule. **Recorded in:** `internal/cli`, `docs/SAFETY_MODEL.md` §4.2, `docs/CLI_UX.md`, `README.md`.

## Context

`reclaim` only listed suggestions. Getting space back meant a second command (`apply --safe`) and a typed plan code, which is more ceremony than the work deserves for caches that rebuild on demand. The owner wants `reclaim` to feel like a one-step cleaner. The dashboard already has a precedent: since 2026-10-28 SAFE and CAUTION items are removed after a second Enter and only RISKY items need typing (`docs/SAFETY_MODEL.md` §4.2).

## Decision

1. At a real terminal (stdin and stdout are TTYs, not `--json`, not `--quiet`, not `--dry-run`), `macdoc reclaim` scans if needed and lists everything by risk, then shows a section "What do you want to clean?" with a row each for SAFE, CAUTION, RISKY and ADVICE (item count, size, a short note).
2. The person types the risk types to clean (`safe`, `caution`, `risky`, or `all`) and presses Enter to submit, which starts the cleanup. Enter alone means SAFE only; `n` cancels; an answer it does not understand cancels and changes nothing. CAUTION is cleaned only when named. A plan is sealed and run through the executor with every existing re-check, quarantine-then-delete step and audit entry.
3. Not at a terminal, with `--json` or `--quiet`, or with `--dry-run`, `reclaim` stays a read-only list. This is what agents see.
4. `reclaim [toolchain]` limits both the list and the cleaning to that toolchain. `--tier X` skips the question and cleans only that tier on Enter, as before. `--toolchains` and `--categories` stay read-only reports.
5. The choice is made in a checkbox list (↑↓ move, space ticks, `a` ticks all, Enter starts, Esc cancels; SAFE starts ticked). Where there is no raw terminal, a typed answer (`safe`, `caution`, `risky`, `all`) does the same. RISKY is cleaned only when ticked, and then it keeps apply's full confirmation: the plan is shown, each RISKY item asks y/N, and the plan's code is typed. One Enter never approves RISKY. ADVICE rows are shown but can never be chosen (macdoc has no action for them), and `all` never includes them. Administrator steps and Claude-proposed plans are never cleaned this way.
6. There is still no `--yes` and no flag that skips the prompt. IDs, not paths, are what the plan holds (ADR 0002), and the executor remains the only mutator (ADR 0003).
7. New SAFE recipes support the command: browser caches (Safari, Chrome, Edge, Brave, Arc, Firefox; caches only, never profiles, history, cookies or passwords) and developer caches (Go build, Cargo registry, Bun, Deno, Composer, Swift PM, node-gyp, pre-commit, VS Code and Cursor caches, JetBrains logs only). JetBrains caches and AI CLI caches are intentionally left out.

## Consequences

- A person gets space back with `macdoc reclaim` and one keypress, still only at a real terminal (ADR 0001 holds: no non-interactive path exists).
- The confirmation for SAFE items (the default answer) is weaker than a typed code. This is accepted because SAFE items are caches that rebuild, the list is shown first, and every item is re-validated, quarantined and audited.
- `apply` stays for specific IDs, other tiers (CAUTION through the typed flow, RISKY), and sealed or Claude-proposed plans.
- Caches are marked `regenerable`, so using one recently does not move it from SAFE to CAUTION (the 7-day rule); otherwise a Mac in daily use would show nothing to clean. This applies to SAFE recipes only, and an app that must be closed (a browser, an editor) is still checked before cleaning. Test: `internal/engine/tests.TestRegenerableCachesStaySafeWhenRecentlyUsed`.
- An agent must never answer reclaim's prompts (`AGENTS.md`). Agents run `reclaim` without a terminal, where it only lists.
- Tests: `internal/cli/tests.TestReclaimEnterCleansSafeItems`, `TestReclaimOtherKeyCancels`, `TestReclaimNeverCleansWithoutATerminal`, `TestReclaimNeverCleansCautionOrRisky`, `TestReclaimChoosesSeveralRiskTypes`, `TestReclaimRiskyKeepsItsConfirmations`, `TestReclaimAdviceCannotBeCleaned`.

## Not decided here

- Cleaning `.DS_Store` files.
- Emptying the Trash.
- Administrator-level system cleanup.
