# Data stores

Part of the [macdoc architecture guide](../../README.md#architecture).

## 4. Data stores

macdoc keeps its state in `~/Library/Application Support/macdoc/`: `state.db`, plan
files, the quarantine folder and `audit.jsonl`. The database is SQLite (pure Go,
WAL mode). Migrations are numbered; each runs in its own transaction with its row in
`schema_migrations`.

| Table | Version | What it holds |
|---|---|---|
| `scans` | v1 | One row per scan: root, start and end time, status (`running`, `complete`, `incomplete`), total bytes and files, unmeasured count, the macdoc version that wrote it. |
| `nodes` | v1 | Folder sizes for a scan. Key `(scan_id, path_fold)`. Only folders at or above the keep threshold (100 MiB) are stored. |
| `unmeasured` | v1 | Paths the scan could not read, with the reason (for example a permission denial). |
| `plans` | v2 | The second copy of each plan's SHA-256, plus origin (`cli` or `claude`), expiry, item count, total bytes, and status (`pending`, `applied`). Plans outlive scan pruning. |
| `quarantine` | v3 | Quarantine entries: plan and item IDs, recipe, original path, bytes, times, status (`pending`, `done`, `canceled`, `restored`, `purged`). |
| `vitals` | v4 | Memory and disk samples (at most one per five minutes), used for swap history in `slow`. |

Retention: the ten newest complete scans, plus the newest scan of each calendar week for
thirteen weeks (`store.PruneScansWeekly`).

Other state on disk:

- **Plan files** (`plans/`): the sealed plan content. The store's hash must match.
- **Quarantine folders**: each entry is `quarantine/<Q-id>/` with `manifest.json` and a
  `payload/` folder holding the moved item.
- **`audit.jsonl`**: one JSON record per line, synced to disk before the write returns.
  Events include plan applied, item done, item skipped, item failed, restore and purge.
- **`config.json`**: settings and extra protected folders (section 11).
