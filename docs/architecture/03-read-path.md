# Read path

Part of the [macdoc architecture guide](../../README.md#architecture).

## 5. The read path

### 5.1 Scan

`scanner.Scan` walks the Data volume and measures allocated space.

- **Bounded memory.** The full tree is never held in RAM. Directories are read in batches
  of 512 names, and results stream into the store.
- **Background priority.** The crawl runs at low priority, so the Mac stays usable.
- **Hard links.** A link set (`LinkSet`) counts each inode once, with a limit of one
  million tracked links.
- **Unreadable places** are recorded in `unmeasured` with a reason and never counted as
  zero. The most common reason is a permission denial, which is why Full Disk Access
  matters (see the MCP warning in `internal/mcp/tools.go`).
- **Devices and symlinks.** The crawl does not cross onto another device (`st_dev`), and it
  never follows symlinks.

`engine.Scan` wraps the crawl, writes the scan row, and computes the summary.

### 5.2 Probes

`internal/probe` reads the Mac's state through macOS tools and parses their output:

| Reading | Source |
|---|---|
| Hardware model, chip, RAM | `sysctl`, `sw_vers` |
| Memory pressure and free percentage | `memory_pressure`, `vm_stat` |
| Swap | `sysctl vm.swapusage` |
| Processes and resident memory | `ps` |
| Disk containers, purgeable space | `diskutil` (APFS list and info) |
| Time Machine local snapshots | `tmutil` |

Each parser is fixture-tested with real output (`probe/tests/testdata/`). Missing or
unparsable values become *unknown*, never zero.

### 5.3 Projects

`internal/projects` finds developer projects from the latest scan (folders with build
output such as `node_modules`, `Pods`, `build`, `.venv`, `DerivedData`). For each it
records idle time (last commit, file change or build), artifact size and git state.
Projects inside protected folders are listed but never suggested for cleanup.

### 5.4 Opportunities

`engine.Opportunities` is the bridge from facts to suggestions:

1. Load the recipe catalog (`recipes.Load`, embedded in the binary).
2. Run each recipe's detector against the stored scan (`engine.ScanNodes` adapts
   the store to `recipes.NodeSource`).
3. Apply the safety policy to every candidate.
4. Rank, drop duplicates and ancestors, and assign stable IDs (`engine.AssignIDs`,
   `OpportunityID`).

The result is an `OpportunityResult`: each item has a risk tier, a method, a size with
its source, the restore cost, and whether it is advice-only.

### 5.5 Engine views

`engine` exposes the functions the interfaces need, and nothing else:

| Function | Used for |
|---|---|
| `Overview`, `Doctor`, `Findings` | the summary and the health checks |
| `Storage`, `Categories` | "where your space went", with System Data decoded |
| `Opportunities`, `FindOpportunity`, `ByToolchain` | suggestions, `explain`, `dev` |
| `Projects` | the projects view |
| `Slow`, `SlowFrom`, `SlowVerdict`, `SlowSummary`, `TipsFor` | memory verdict, swap history, tips |
| `Trend`, `SpanText` | growth between two stored scans, with honest ranges |
| `CreatePlan`, `ShowPlan`, `ListPlans`, `LockPlans` | plan storage (section 7) |
| `AuditLog`, `Quarantine`, `FreeSpace`, `LastScan` | history and status |
