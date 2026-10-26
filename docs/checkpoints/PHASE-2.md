# Checkpoint: Phase 2 (Full-crawl scanner + store → `macdoc scan`) ✅

**Date:** 2026-10-27 · Not a user-review milestone (the next one is after Phase 3).

## Built
- **`internal/scanner`** (P2-W1): a parallel, read-only crawl of the Data volume.
  - Measures **allocated** bytes, so sparse files like `Docker.raw` count correctly.
  - Hard links are counted once, symlinks are never followed, and it stays on one device.
  - A bounded LIFO work stack with streaming bottom-up aggregation: memory doesn't grow with file count.
  - Clean cancellation and progress updates.
  - `SetBackgroundPriority()` puts the process in the PRIO_DARWIN_BG band (throttled CPU and I/O).
  - Coverage 95.3%.
- **`internal/store`** (P2-W2): a SQLite (pure-Go) state database, mode 0600, WAL.
  - Migrations; scans, nodes and unmeasured paths; case-insensitive prefix queries; history; pruning.
  - Coverage 91.8%. 50k nodes are written in about 0.75 s.
- **`macdoc scan [--json] [--foreground] [--workers N]`** (tech lead):
  - Live progress, and Ctrl-C saves a partial scan.
  - Breakdown by top-level folder, plus "where the space actually is".
  - Says how much of the Data volume it could see.
  - Hints at Full Disk Access when locations can't be read.
  - Keeps the last 10 scans.

## Real Mac result (first full scan)
| Metric | Result | Target |
|---|---|---|
| Files | 994k | n/a |
| Time | **40–41 s** at background priority | n/a |
| Peak RSS | **24.7 MB** | ≤ 64 MB |
| Scanner peak heap (benchmark, 100k–200k files) | **4.3 MB**, flat | ≤ 48 MB |
| Measured vs. Data volume used | 124 GB of 159 GB | ±3% or explained |

**Explained gap (35 GB):** 166 locations need Full Disk Access (Mail, Messages, app containers, protected system folders), plus deliberately skipped system indexes. `scan` now prints this gap. Granting Full Disk Access to Terminal should close most of it.

**Early signal for Phase 3:** about **49 GB** of iOS Simulator runtimes (33.7 GB) plus their dyld cache (15.2 GB). Also 3.3 GB of `~/Library/Caches`, 4.3 GB of Homebrew Cellar, and 27 GB in `~/Library/Application Support`.

## Tasks
- P2-W1 and P2-W2 ran in parallel. P2-W1 needed one resume: it put its benchmark in the background, and in headless mode that ends the session. Fixed by a foreground rule in `run-worker.sh`.
- Tech-lead fixes after the real-data run:
  - "Where the space actually is" excludes the root and top-level folders.
  - The scan summary states Data-volume coverage.
