# S1: does APFS private size predict what a delete frees?

**Question.** The scanner sizes everything as `st_blocks * 512`. On APFS, a clone shares blocks with its original, so that number can be far above what deleting the file frees. `docs/DEVELOPMENT_PLAN.md` says clones "can't be detected cheaply". The macOS SDK (`sys/attr.h`) defines `ATTR_CMNEXT_PRIVATESIZE`, `ATTR_CMNEXT_CLONEID` and `ATTR_CMNEXT_CLONE_REFCNT`. Do they predict the real freed space?

**Oracle.** The kernel. For each scenario the driver reads free space with `statvfs` on a throwaway APFS volume, deletes the files, and reads it again. That difference is ground truth. Nothing in the driver trusts macdoc.

## Run it

```sh
clang -O1 -Wall -o attrs attrs.c
hdiutil create -size 1g -type SPARSE -fs APFS -volname macdocoracle oracle
mkdir mnt && hdiutil attach -nobrowse -mountpoint "$PWD/mnt" oracle.sparseimage
python3 -I oracle.py "$PWD/mnt" "$PWD/attrs"
hdiutil detach "$PWD/mnt"
```

`oracle.py` refuses to run unless the mount point is a separate device from its parent directory and `diskutil` reports APFS, so its deletes can only touch the disk image. Never point it at a real volume.

## Result (macOS 26, Darwin 25.6, 2026-11-06)

MiB. "freed" is the measured truth.

| Scenario | freed | `st_blocks` (current) | `PRIVATESIZE` | clone-group aware |
|---|---:|---:|---:|---:|
| 1 unique 64 MB file | 64 | 64 | 64 | 64 |
| 2 clone original (2 clones remain) | 0 | **64** | 0 | 0 |
| 3 one of 2 remaining clones | 0 | **64** | 0 | 0 |
| 4 last clone | 64 | 64 | 64 | 64 |
| 5 one of two hardlinks | 0 | 64 | 64 | 64 |
| 6 last hardlink | 64 | 64 | 64 | 64 |
| 7 sparse 200 MB (4 MB written) | 4 | 4 | 4 | 4 |
| 8 diverged clone (8 MB rewritten) | 8 | **64** | 8 | 64 |
| 9 original after its clone is gone | 64 | 64 | 64 | 64 |
| 10 whole clone group (3 files) at once | 64 | **192** | 0 | 64 |

## Reading it

- `st_blocks` is wrong in scenarios 2, 3, 8 and 10 (up to 3x too high). It is right only when nothing is shared.
- `PRIVATESIZE` is exact for every single-file scenario except hardlinks (5). That case is about link counts, not clones: freeing needs every link in the set. The scanner already de-duplicates by `(dev, ino)`.
- `PRIVATESIZE` fails for a set that holds a whole clone group (10): each member reports 0 because the blocks are shared among them. The shared blocks belong to nobody individually, so a plan needs clone-group logic, or an honest range: low = sum of private sizes, high = sum of allocation with each clone group counted once.
- `CLONEID` and `CLONE_REFCNT` come back in the same call as the size, so detecting clones is not expensive. The speed question (bulk vs one syscall per entry) is separate; see H5 in `plan.md`.

## Directories (2026-11-06)

The first run covered files only. `dirtest.py` repeats the method for whole directories: build a tree, delete it, compare the freed space with two predictions. A directory contained a unique file, clones of a file that stays outside it, or a clone group that lives entirely inside it.

| Directory contents | Actually freed | `st_blocks` sum | Sum of private sizes |
|---|---:|---:|---:|
| unique file + clone of a file outside | 32 MB | 64 | **32** |
| a clone group entirely inside | 32 MB | 96 | 0 |
| two clones of a file outside | 0 | 64 | **0** |
| mix of the three | 32 MB | 80 | 16 |

`st_blocks` overstates in every case. The sum of private sizes never overstates, so it is a safe **lower bound**; it undercounts only when a clone group sits wholly inside the directory. Counting each clone group once, by clone ID, gives an upper bound (48 for the mix), and the true value was inside the range in all four cases.

A directory reports a private size of its own (`dirattr.c`), but that is only the directory's own blocks (0 for a directory holding a 5 MB file), not a recursive total. A scan therefore has to sum the files' private sizes up the tree, as it already does for allocated bytes.

Timing: on a real tree (`/opt/homebrew`, 322,511 files) reading with `getattrlistbulk` took about 2.3 s against 4.0 s for one `fstatat` per entry, in 12 alternating runs, with identical results.

## Limits

One 1 GB image, 64 MB files, one macOS version, files only. Not tested: directories, snapshots, purgeable space, compressed files, Intel. Do not treat this as a final model of reclaimable space; it is the evidence needed to decide whether to build one.
