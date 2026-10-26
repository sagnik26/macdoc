# Checkpoint: Phase 1 (System probe → `macdoc doctor`) ✅

**Date:** 2026-10-27 · Not a user-review milestone (those are after Phases 0, 3, 6, 7 and 9).

## Built
- `internal/probe` reads:
  - hardware (sysctl, sw_vers)
  - memory (vm_stat, memory_pressure, pressure level)
  - swap
  - top memory users, grouped by app and dev tool: Docker, Android Emulator, Node.js, Ollama, Xcode, Android Studio, iOS Simulator, QEMU VMs
  - the APFS container and its volumes (diskutil plists)
  - local Time Machine snapshots
- `internal/platform/darwin`: optional cgo shim that reads **purgeable space** through the Foundation API. Without cgo it's reported as unknown.
- `internal/engine.Doctor` + `Findings`: turns probe failures into warnings and the numbers into plain-language findings.
- `macdoc` / `macdoc doctor [--json]`.

## Real Mac result (2026-10-27)
MacBook Air M1, 8 GB, macOS 26.6.2:
- 186 / 245 GB used, 58.7 GB free, +2.9 GB purgeable
- swap 3.9 GB with normal pressure
- top memory users: Chrome 1.5–2.2 GB, Cursor ~1 GB
- 0 snapshots

`doctor` runs in **1.3 s** with **16 MB RSS**.

## Tasks
- P1-W1 (hardware/memory/swap/ps) and P1-W2 (disk/APFS/snapshots/purgeable) ran **in parallel**. Each merged after one round.
- The tech lead wrote `doctor`, plus two fixes found on real data:
  - Swap under normal pressure is now informational.
  - QEMU VMs get their own group.
