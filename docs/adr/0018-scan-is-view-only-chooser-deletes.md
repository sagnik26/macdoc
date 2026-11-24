# 0018: The scan only shows; deleting a single item is `/clean` and `reclaim --choose`

**Status:** Accepted (owner decision, 2026-11-24: "implement the whole plan", after the plan and the flow were read and confirmed; RISKY items are in the list, deleted on Delete with no code, as in Retire). Implemented. **Recorded in:** `internal/cli/choose.go`, `internal/cli/itempicker.go`, `internal/cli/removal.go`, `internal/cli/dashboard.go`, `internal/cli/reclaim.go`.

## Context

The scan's Suggestions tab was also a place to delete: `space` ticked rows, Enter opened a removal box, and SAFE rows were ticked for you after a scan. So there were several ways to delete (`/reclaim`, Retire, and the scan), each with its own prompts, and `/reclaim` could not pick a single item at all. A person who wanted to delete one RISKY folder had to do it from the scan.

## Decision

1. **The scan is view-only.** On the Suggestions tab Enter and `e` show or hide the explanation of the selected item. There is no `space`, no tick column, no marked count, no "enter remove" and no pre-ticked row. The footer points to `/clean`. Nothing on any scan tab makes a plan or removes anything.
2. **`/clean` is where one item at a time is deleted** (dashboard). It lists what the latest scan found that macdoc can act on, with nothing ticked at the start. `space` ticks a row, `a` ticks all or none, Enter seals a plan for exactly the ticked rows and opens the Delete / Cancel box (the one Retire uses, with each row's risk label and path, and the total). Enter with nothing ticked says so and plans nothing: it never deletes the highlighted row. Advice-only and blocked items and installed apps (those are for Retire) are not listed, as `/reclaim` leaves them out. Without a scan, `/clean` runs one first.
3. **`macdoc reclaim --choose` is the terminal twin.** At a real terminal it shows the same list as a tick list; with a faked terminal it prints a numbered list and reads the numbers typed. It then shows the plan and asks `y/N`; only `y` deletes. It is refused with `--yes` and `--dry-run`, and when piped or with `--json` or `--quiet`.
4. **`/reclaim` and `macdoc reclaim` are unchanged:** the dashboard's `/reclaim` still cleans SAFE and CAUTION with no prompt after its own scan (ADR 0014), the terminal command keeps the tier checkboxes, `--tier` and `--yes`. The summary after `/reclaim` gains one hint, `c choose items`, which opens `/clean`; its `d review RISKY` step keeps the typed IDs.
5. **RISKY needs no typed code in `/clean` and `--choose`.** The person ticked the row (or typed its number) and pressed Delete (or `y`) with its RISKY label and path on screen, which is its own approval; the executor receives it as an individual approval, so it is not a group approval. This is the same decision as ADR 0017 for Retire. The typed ID stays in the one place that deletes RISKY items in bulk, `/reclaim`'s `d` review.

## What does not change

Only the executor deletes. Every item is re-validated just before it is moved, the protected paths are refused, and each deletion is quarantined and then deleted at once and written to the audit log. Plans take opportunity IDs, never paths; `/clean` builds its selector from the IDs of the rows ticked. There is still no MCP tool that plans or deletes, and no `--yes` for `--choose`. A plan Claude proposed can never be confirmed with a `y` here.

## Consequences

- One mental model: scan looks, `/reclaim` cleans by risk type, `/clean` and `--choose` delete what you pick, `/retire` removes programs.
- A stray `space` can no longer tick anything on the scan, and no Enter there can start a deletion.
- It supersedes the part of ADR 0013 and ADR 0015 that described deleting from the scan's Suggestions tab, and the typed-code step of ADR 0001 for items picked one by one.
