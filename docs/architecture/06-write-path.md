# Write path

Part of the [macdoc architecture guide](../../README.md#architecture).

## 8. The write path

### 8.1 Apply, end to end

`executor.Apply(ctx, cfg, plan, approved)`:

1. Check the sandbox (`sandbox.Root`), the state directory permissions, and that some
   approval exists.
2. Build a *fresh* policy from the config, so protected folders that became symlinks
   since the plan are covered at their real location.
3. Load the plan from the store and `planner.Verify` it.
4. Tighten the state directory's permissions.
5. Mark the plan applied (the lock, step 4 of section 7).
6. Measure free space, and write `apply_started` to the audit log. If the audit log
   can't be written, nothing changes.
7. For each item, in `planner.Perform` order, run the per-item pipeline (8.2).
8. Measure free space again and write `apply_finished` with expected and actual bytes.

### 8.2 The per-item pipeline

`performer.do` handles each item:

1. **Approval.** The item must be approved. A group approval is not enough for a
   Claude plan or a RISKY item; each of those needs its own confirmation.
2. **Common checks** (`performer.common`): the recipe still exists, its method has not
   changed, every precondition is known, and every precondition holds
   (`precond.Checker`, for example "the owning app isn't running").
3. **Act:**
   - `quarantine`: `move.go` re-checks the target (canonical path, fingerprint, the
     folder's contents against the plan, the allow list and the policy), then renames it
     into a new quarantine entry with `renameatx_np`, writing the manifest first.
   - `native`: `native.go` runs the recipe's fixed argv with a timeout. Before running, it
     checks that the tool still lists the identifier it's about to remove. The output is
     captured and recorded.
4. **Audit.** Every outcome (done, skipped, failed) is appended to `audit.jsonl` with the
   argv, the target, the exit code and the reason.
5. **Optional immediate delete** (`DeleteNow`). The quarantine entry is purged right away
   through the same checks as a manual purge, so the space is freed at once. If the purge
   is refused, the item stays restorable and a warning says so.

Any mismatch between plan and reality is a skip with a reason, never a "fix-up".

### 8.3 Quarantine: restore and purge

- `manage.go` restores an entry to its original path (refusing if something now occupies
  it), and purges entries.
- A purge removes the payload before the manifest, so an interrupted purge always leaves
  the manifest to account for it. The crash-order rule is tested.
- `macdoc quarantine list` shows entries. `macdoc quarantine purge --expired` purges
  entries that have passed their expiry; `purge <Q-id>` purges a named entry.
- `executor/schedule.go` writes and removes the launchd agent file for `schedule
  enable|disable`. It never runs `launchctl`; the person does.
