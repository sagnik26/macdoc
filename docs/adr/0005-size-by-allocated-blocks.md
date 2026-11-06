# 0005: Sizes come from allocated blocks (`st_blocks`)

**Status:** **Accepted for change, in stages** (owner decision, 2026-11-06). The evidence below shows the current method is wrong for APFS clones. Stage 1 adds a private-size column filled from the bulk read and leaves `bytes` as it is; stage 2 shows reclaimable as private size and costs plans with it (built, see below); stage 3 shows a range for a suggestion whose files share space (built, see below). **Recorded in:** `internal/scanner/scanner.go`; `docs/DEVELOPMENT_PLAN.md` line 155 ("APFS clones: can't be detected cheaply").

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

`nodes.private_bytes` (migration 5) and `model.Node.Private` (`*int64`, not in any JSON) hold the lower bound for each kept node. The bulk read asks for `ATTR_CMNEXT_PRIVATESIZE` through `forkattr` with `FSOPT_ATTR_CMN_EXTENDED`. Files add their private size, counted when their bytes are (a hard link once; stage 2 makes a multi-link file add a known 0); a folder adds its own allocation, so a tree with no clones has private equal to bytes.

**Unknown.** A file is unknown when the kernel did not report the attribute (the fstatat reader, or a volume that does not return it; HFS+ and exFAT were checked and return nothing, while the bulk read itself still works there). A node is unknown if any file below it is, and it stays unknown all the way to the root; a folder with no unknown file below it stays known. Unknown is stored as NULL and read back as nil, never as 0, and rows from scans made before the column existed are NULL. (Stage 1 printed and costed nothing from this value; stage 2 does.)

**Cost (measured 2026-11-06, APFS, 12 cores).** Asking for the private size is not free, and it blows the 5% budget. On `/opt/homebrew` (322,511 files) a scan went from 1.52 s to 2.27 s wall (+49%) and from 2.2 s to 4.7 s CPU; on a synthetic tree of 100,000 small files from about 100 ms to 290 ms. The cost is about 2 microseconds of wall time per file whatever its size, and it sits in the kernel's private-size attribute: `FSOPT_ATTR_CMN_EXTENDED` alone is free (1.50 s), and clone ID plus clone reference count cost +5 to 8% (1.58 s). Peak memory is unchanged (about 14 MB resident, 4.3 MB heap). Bytes, files and kept nodes are identical to before on `/opt/homebrew` (10,852,208,640 bytes, 322,511 files, 3,434 nodes).

**Known gap.** For a file with several links, the kernel reports the full size for each link, so a tree that holds only some of the links of an inode counts bytes the delete would not free (S1 scenario 5). Bytes has the same flaw today. The lower-bound claim holds for clones; it does not yet hold for partly-contained hard links. Stage 2 settles that before it shows the number (see "Stage 2 as built").

## Stage 2 as built

A suggestion made from a scan node is sized by `model.Node.Reclaimable()`: the node's private size when known, else its allocated bytes (never above bytes). That covers every recipe built on `nodeFinding` (folders, files, caches, DerivedData, device support, Android and node versions), the unavailable-simulators sum, and project artifacts and DerivedData found by project discovery. Sizes a tool reports (Docker, Homebrew, conda, Ollama, simctl's own `dataPathSize`) are untouched. Plans take `expected_bytes` from the opportunity, so they are costed the same way. `min_bytes` thresholds, staleness and the version-control check gate in project discovery still read the allocated size, so which folders a recipe looks at does not change; only the number shown does.

**Hard links.** The kernel reports the full size for every link, so a tree holding some of an inode's links would overstate (scenario 5). The scanner now counts a file with link count above 1 as a known 0 in the private sum (its bytes still count once, as before). A tree that holds every link of an inode therefore understates, which is the safe side. A file the kernel reported no private size for stays unknown even if it has several links.

**A known 0.** A folder whose files are all clones of each other, or of files elsewhere, has a private size of 0 and is shown as 0 B, source measured. The default 1 MB display threshold applies to the number shown, so such a suggestion is hidden and counted in the note "N item(s) that would free under 1 MB not shown"; with the threshold off it is listed last. Totals are sums of the lower bounds.

**Reporting.** The expected size in the apply report is now a floor, so the freed space is normally at or above it; the report says so when it is, and names shared space among the reasons for a shortfall. One gap: for a SAFE item that changed after the plan, the executor re-measures the folder's allocated bytes (`checkContents`), so that item's moved and audited bytes are allocated, not the lower bound, and the audit's "finished" record can mix the two. This was left alone because it is executor code.

**JSON.** No field was renamed or removed. `bytes` of an opportunity changes meaning, as above. `doctor projects` keeps `bytes` as allocated and adds `reclaimable_bytes` (artifacts and DerivedData, omitted when unknown; projects, always). `macdoc scan`, `doctor storage` and the other views of what is on disk still show allocated bytes.

**Still open.** The apply report's new sentences have no test, because the report function takes a measured free-space change that the test harness does not script.

## Stage 3 as built

`doctor explain` can say what removing a suggestion frees as a range: "at least X and at most Y". The size shown in lists, totals and plans is still the lower bound (`wins`, the dashboard and plan costing are unchanged); only the explanation adds the upper end.

**On demand, not in the scan.** Holding clone IDs for every file of a disk would break the scanner's memory budget, and asking for them costs the scan 5 to 8%, so the full scan is unchanged. `engine.Measure` (in `internal/engine/span.go`) walks the one folder being explained with `scanner.Measure`, which reads the same getattrlistbulk records through the scan's own walker (same rules: no symlinks followed, no other device, hard links once) and additionally asks for the clone ID and clone reference count. It runs only when all of these hold: the suggestion is a measured quarantine folder; a node of the suggestion's own scan has exactly that path (so a path the scan never stored is never read, and nothing takes a path from a flag or a tool); the node's size is the suggestion's size; and its allocated bytes exceed its private size by more than 1% and more than 1 MB (`engine.ShareFloor`), because otherwise the two ends cannot differ by enough to matter. It has a 3 minute limit and stops on cancel. A walk that fails costs the range, not the explanation (a warning says so).

**What the two ends are.** *Lower* is the folders' own blocks plus the sum of the files' private sizes, a file with several links counting 0. It equals the scan's private size for the folder to the byte (checked on `/Applications`, `/opt/homebrew` and a 101 GB home folder). *Upper* is the smaller of two bounds, both of which hold for any pattern of sharing:

1. Files with the same clone ID hold the same data (a file that is rewritten gets a new ID; measured). A group whose members are all inside the folder (members seen reach the reference count) counts as one copy, the largest member. A group with a member outside the folder counts only its members' private sizes, since the outside member keeps the data.
2. The total allocation less half of the shared space (allocated less private, over files with one link). Every block that frees with two or more holders inside the folder appears in the shared part of at least two files. This covers chains of rewritten clones, whose blocks are shared across different IDs, which rule 1 alone counts more than once.

Hard-linked inodes count once in the upper end, whether or not every link is inside.

**What it promises.** The true freed amount lies between the ends, apart from space that snapshots or open files still hold. It does not promise the ends are close: a chain of rewritten clones can leave the upper end well above the truth (12 MB freed, 17 MB upper in the test), and a hard link whose other link is outside leaves it at the inode's full size (the lower end is exact there). The upper end can be below the allocated bytes by a lot: on `/Applications` 1.6 GB is shared with files outside the folder, and the range is 0.45 MB wide.

**When there is no upper end.** More clone groups than `scanner.CloneLimit` (250,000, about 15 MB; the cap was chosen against the 64 MB budget, not the 1,000,000 first suggested), any folder that could not be read (its files are missing from both ends), or a file the kernel reported no private size for (then no range is shown at all). With no upper end the text says "at least X" and JSON carries `bytes_lower` alone. Unknown is never written as a number.

**Wording and JSON.** With a range: "Removing this frees at least 1.2 GB and at most 1.9 GB. The difference is space shared with copies of its files." Without an upper end: "Removing this frees at least 1.2 GB. The most it could free could not be worked out." When the ends are equal or no walk was made, the stage 2 sentence stays. `explain --json` gains the optional `bytes_lower` and `bytes_upper` (omitted, never null, when not measured; additive, `schema/explain.json` regenerated). The MCP server and the dashboard's explain pane do not walk, and show the stage 2 text.

**Verified against the kernel.** `TestTruth` (opt-in, `MACDOC_ORACLE_VOLUME`) builds each scenario on a throwaway APFS volume, deletes it and compares the free-space change with the range. All ten fell inside, MB freed with [lower, upper]: unique 8 [8, 8]; unique plus clone of an outside file 8 [8, 8]; clone group inside 8 [0, 8]; two clones of an outside file 0 [0, 0]; mix 8 [4, 8]; hard links all inside 8 [0, 8]; hard link to a file outside 0 [0, 8]; rewritten clone beside its source 10 [4, 10]; chain of rewritten clones 12 [2, 17]; rewritten clone of an outside file 3 [3, 5.5].

**Cost (2026-11-06, APFS, 12 cores).** The walk costs the same as a scan of the folder (the clone attributes were free next to the private size): `/Applications` 0.21 s (48k files, 12 MB resident), `/opt/homebrew` 2.3 s (322k files, 12 MB), `~/Documents` 5.6 s (725k files, 101 GB, 17 MB).

## Consequences of leaving it as is

Reclaimable totals can be several times too high on a machine full of clones (Xcode and simulator folders are the common case). That affects the person's decision, not the safety of the deletion.
