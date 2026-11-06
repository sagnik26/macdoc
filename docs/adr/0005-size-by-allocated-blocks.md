# 0005: Sizes come from allocated blocks (`st_blocks`)

**Status:** **Accepted for change, in stages** (owner decision, 2026-11-06). The evidence below shows the current method is wrong for APFS clones. Stage 1 adds a private-size column filled from the bulk read and leaves `bytes` as it is; stage 2 shows reclaimable as private size and costs plans with it; stage 3 gives sets with clone groups a range. **Recorded in:** `internal/scanner/scanner.go`; `docs/DEVELOPMENT_PLAN.md` line 155 ("APFS clones: can't be detected cheaply").

## Context

The scanner measures every file as `st_blocks * 512`, the bytes allocated to it, which is what `du` reports and is cheap to read during the crawl.

## What was measured

On a throwaway APFS volume (`docs/experiments/S1-apfs-private-size`), comparing predictions with the kernel's own free-space change after deleting:

- `st_blocks` is right when nothing is shared, and wrong for clones: it reported 64 MB for a clone whose deletion frees 0, 64 MB for a clone with 8 MB of its own data, and 192 MB for three clones that free 64 MB together.
- The macOS SDK returns `ATTR_CMNEXT_PRIVATESIZE` (bytes freed if this file were deleted), a clone ID and a clone reference count through `getattrlist` and `getattrlistbulk`, so the claim that clones cannot be detected cheaply is false. Private size was exact for every single-file case except hardlinks.
- A set of files that share a clone group cannot be summed: each member's private size is 0 and the shared blocks belong to nobody. A plan needs a range (sum of private sizes up to allocation counted once per clone group).
- On `/opt/homebrew`, bulk reads gave the same total as per-entry `fstatat` to the byte with 5.3 times fewer syscalls; CPU time was about equal. (Timings were taken under high system load and must be repeated.)

## Options

1. Keep `st_blocks` and label sizes "may include cloned blocks" where clones are common (the cheapest change; keeps over-stating).
2. Store both allocated and private bytes; show reclaimable as private size, and show a range for sets with clone groups. Needs a schema migration, a scanner change and a change to how plans are costed.

## Decision

Option 2, in three stages. Measured since this record was opened: directories behave the same way (the sum of the files' private sizes never overstates and is a lower bound; counting each clone group once is an upper bound; the true value was inside the range in all four tested cases), and reading with `getattrlistbulk` is faster than one `fstatat` per entry on a real tree (2.3 s against 4.0 s). A directory's own private size is only its own blocks, so the scan must sum files up the tree.

## Consequences of leaving it as is

Reclaimable totals can be several times too high on a machine full of clones (Xcode and simulator folders are the common case). That affects the person's decision, not the safety of the deletion.
