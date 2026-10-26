# macdoc

> A safe, local-first macOS CLI that explains why a developer Mac is full and slow, and gets space back without surprises.

**Status:** planning (no code yet) · **Project started:** 2026-10-26 · `macdoc` is a working name

---

## What it is

macdoc is a **storage doctor for developer Macs, with a built-in explanation of why the Mac is slow.** It:

- **Diagnoses** the machine: hardware, APFS disk and container usage, purgeable space, local Time Machine snapshots, swap and memory pressure.
- **Explains where storage went**, including the opaque "System Data" bucket, broken down by real causes: Xcode, Android, Node/React Native, Docker, Python, local AI models, Homebrew and stale projects.
- **Finds reclaimable space.** Every item comes with an honest size, a risk level (SAFE / CAUTION / RISKY / ADVISE-ONLY), how long it's been idle, and what it costs to get it back.
- **Plans cleanups as dry-runs first.** Nothing is removed until you review a sealed plan and confirm it in your terminal.
- **Cleans safely.** It uses each tool's own cleanup command (`brew cleanup`, `xcrun simctl`, `docker builder prune`, …), or moves items to a restorable quarantine, and keeps an audit log of every action.
- **Connects to Claude later** through a local MCP server. Claude can diagnose and *propose* cleanup plans, and only you can apply them.

It is **not** a "Mac cleaner" in the usual sense. It doesn't run a background daemon, delete things automatically, "boost RAM", kill processes or report a fake health score.

## Why we need it

The target machine is an **8 GB MacBook used for full-stack and mobile development** (Xcode, Android Studio, React Native, Node.js, Homebrew, Docker, npm/pnpm/yarn, Python, local AI models). Machines like this run into a predictable spiral:

```
Low RAM ──▶ heavy swap ──▶ swap files grow on disk ──▶ free space shrinks
   ▲                                                         │
   └──── macOS hangs, builds fail, updates won't install ◀───┘
```

Developer tooling quietly eats tens of gigabytes. A typical dev Mac carries:

| Source | Typical size |
|---|---|
| Docker Desktop disk image, build cache, images | 20–60 GB |
| Xcode DerivedData, simulator runtimes, DeviceSupport, Archives | 20–80 GB |
| Android emulators, system images, old SDKs, Gradle caches | 15–50 GB |
| Local AI models (Ollama, Hugging Face, LM Studio), often duplicated | 10–100 GB |
| `node_modules`, Pods and build folders in projects you no longer touch | 5–40 GB |
| Package-manager caches (npm, pnpm, yarn, pip, uv, CocoaPods, Homebrew) | 5–30 GB |
| Local snapshots and swap, shown only as "System Data" | 5–30 GB |

**Existing tools don't solve this well:**
- **Finder / System Settings › Storage** shows big opaque buckets ("System Data", "Developer") with no explanation and almost no actions.
- **Generic cleaner apps** don't understand developer tooling. They either miss most of the space or delete things blindly. They also tend to use scare tactics, subscriptions and background agents.
- **Manual cleanup** means remembering dozens of paths and commands, and it's easy to delete something you need (Xcode Archives with dSYMs, Docker volumes, a venv with local changes).

## Why we're building it this way

1. **Developer-aware, not generic.** It knows that DerivedData is regenerable but Archives hold irreplaceable dSYMs, and that a system image no AVD uses is dead weight.
2. **Honest numbers.** It distinguishes "size on disk" from "space you'll actually get back" (APFS clones, sparse files, snapshots, purgeable space). Unknowns are shown as unknowns.
3. **Safety is the product.** It's read-only by default. Actions come only from a fixed recipe catalog and never from arbitrary paths. Every cleanup is a dry-run first and needs confirmation in the terminal. It prefers each tool's own commands, uses a restorable quarantine, and audits everything.
4. **Built for low-RAM hardware.** Scans run at background priority with bounded memory (≤ 64 MB target), so the tool never makes a struggling Mac worse.
5. **Ready for an AI assistant without handing over control.** Claude gets rich, structured diagnosis and can draft plans, but there's no deletion code path reachable from Claude. The human-approval boundary is the terminal.
6. **Local-first and private.** No network calls, no telemetry, no account.

---

## Decisions locked

| Topic | Decision |
|---|---|
| Scope | Build **MVP B** (diagnosis, dry-run cleanup planner, terminal-confirmed apply, audit log). Design it so **C** (Claude via local MCP) is purely additive. |
| Default mode | Read-only diagnosis. Read-only commands never delete anything, including expired quarantine. |
| Removal | Use the owning tool's native command first. Otherwise move the item to a macdoc-managed **quarantine** that auto-expires. |
| Scanning | **Full crawl** of the Data volume every scan, run at low priority with streaming aggregation and bounded memory. |
| Language | **Go**, chosen to ship fast. One static binary, with a tiny optional cgo shim for a few macOS APIs. |
| Audience | Personal first, open-source later. Recipes are data-driven so others can add theirs. |
| Claude boundary | Claude can diagnose and **create plans**. Only a human at a real terminal (TTY) can **apply** them. The MCP process contains no deletion code. |

## Repository layout

```
macdoc/
  README.md            this file: what, why, decisions
  CLAUDE.md            instructions for Claude Code
  AGENTS.md            instructions for any AI coding agent (source of truth for agents)
  docs/
    DEVELOPMENT_PLAN.md
    SAFETY_MODEL.md
    RECIPE_CATALOG.md
    CLI_UX.md
    MCP_DESIGN.md
```

## Documents

| File | What's in it |
|---|---|
| [docs/DEVELOPMENT_PLAN.md](docs/DEVELOPMENT_PLAN.md) | Architecture, project layout, phases and milestones, tasks, acceptance criteria, testing, risks, and estimates |
| [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md) | Risk classes, protected paths, the plan, confirm, apply and verify lifecycle, quarantine, audit log, and threat model |
| [docs/RECIPE_CATALOG.md](docs/RECIPE_CATALOG.md) | The v1 catalog of cleanup recipes, with paths, risk, removal method, preconditions and restore cost |
| [docs/CLI_UX.md](docs/CLI_UX.md) | Commands, flags, report mockups, tone rules, and the JSON output contract |
| [docs/MCP_DESIGN.md](docs/MCP_DESIGN.md) | Phase C: MCP tools, the Claude-to-terminal handoff, and prompt-injection hardening |

## Timeline at a glance (part-time, ~15–20 h/week)

```
Phase 0  Foundations                 ▓░░░░░░░░░░░░░░░░░░░   ~0.5 wk
Phase 1  System probe → doctor       ▓▓░░░░░░░░░░░░░░░░░░   ~1 wk
Phase 2  Full-crawl scanner + store  ▓▓▓▓░░░░░░░░░░░░░░░░   ~1.5 wk
Phase 3  Recipe engine + catalog     ▓▓▓▓░░░░░░░░░░░░░░░░   ~1.5 wk
Phase 4  Projects & staleness        ▓▓░░░░░░░░░░░░░░░░░░   ~0.5–1 wk
Phase 5  Planner (dry-run)           ▓▓░░░░░░░░░░░░░░░░░░   ~1 wk
Phase 6  Executor + quarantine+audit ▓▓▓▓░░░░░░░░░░░░░░░░   ~1.5 wk
Phase 7  slow, trend, polish → v0.1  ▓▓░░░░░░░░░░░░░░░░░░   ~1 wk      ← MVP B done (~8–9 wk)
Phase 8  MCP server (read + plan)    ▓▓░░░░░░░░░░░░░░░░░░   ~1 wk
Phase 9  Handoff + hardening         ▓░░░░░░░░░░░░░░░░░░░   ~0.5 wk
Phase 10 Daily snapshots, TUI, tips  ▓▓▓░░░░░░░░░░░░░░░░░   ~1.5 wk    ← C done (~11–12 wk)
Phase 11 Open-source readiness       ▓▓░░░░░░░░░░░░░░░░░░   later
```
