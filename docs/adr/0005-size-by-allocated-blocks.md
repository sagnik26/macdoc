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

## Stage 1 as built

`nodes.private_bytes` (migration 5) and `model.Node.Private` (`*int64`, not in any JSON) hold the lower bound for each kept node. The bulk read asks for `ATTR_CMNEXT_PRIVATESIZE` through `forkattr` with `FSOPT_ATTR_CMN_EXTENDED`. Files add their private size, counted exactly when their bytes are (a hard link once); a folder adds its own allocation, so a tree with no clones has private equal to bytes.

**Unknown.** A file is unknown when the kernel did not report the attribute (the fstatat reader, or a volume that does not return it; HFS+ and exFAT were checked and return nothing, while the bulk read itself still works there). A node is unknown if any file below it is, and it stays unknown all the way to the root; a folder with no unknown file below it stays known. Unknown is stored as NULL and read back as nil, never as 0, and rows from scans made before the column existed are NULL. Nothing prints or costs from this value yet.

**Cost (measured 2026-11-06, APFS, 12 cores).** Asking for the private size is not free, and it blows the 5% budget. On `/opt/homebrew` (322,511 files) a scan went from 1.52 s to 2.27 s wall (+49%) and from 2.2 s to 4.7 s CPU; on a synthetic tree of 100,000 small files from about 100 ms to 290 ms. The cost is about 2 microseconds of wall time per file whatever its size, and it sits in the kernel's private-size attribute: `FSOPT_ATTR_CMN_EXTENDED` alone is free (1.50 s), and clone ID plus clone reference count cost +5 to 8% (1.58 s). Peak memory is unchanged (about 14 MB resident, 4.3 MB heap). Bytes, files and kept nodes are identical to before on `/opt/homebrew` (10,852,208,640 bytes, 322,511 files, 3,434 nodes).

**Known gap.** For a file with several links, the kernel reports the full size for each link, so a tree that holds only some of the links of an inode counts bytes the delete would not free (S1 scenario 5). Bytes has the same flaw today. The lower-bound claim holds for clones; it does not yet hold for partly-contained hard links. Stage 2 has to settle that before it shows the number.

## Consequences of leaving it as is

Reclaimable totals can be several times too high on a machine full of clones (Xcode and simulator folders are the common case). That affects the person's decision, not the safety of the deletion.
