# Plans

Part of the [macdoc architecture guide](../../README.md#architecture).

## 7. Plans: sealing and verification

A plan is what the user (or Claude) proposes and what `apply` carries out.

1. **Build.** `planner.Build` takes opportunity IDs (never paths), resolves them through
   the same engine view the user saw, applies the policy again, and refuses anything
   unsafe with a reason (`planner.Refusal`).
2. **Seal.** `planner.Save` writes the plan file and stores its SHA-256 in the `plans`
   table. The plan gets an expiry (default 30 minutes; `plan_minutes` in config).
   `planner.Hash` is computed over the canonical content, so any edit changes it.
3. **Verify.** `planner.Verify` runs at apply time. It checks the file against the
   stored hash, the expiry, the schema version, the catalog and the policy. Any mismatch
   stops the apply before anything changes.
4. **Lock.** `executor.Apply` marks the plan `applied` in the store before the first
   item runs. A plan can therefore never be applied twice, even after a crash.

Plan IDs (`P-…`), item IDs (`O-…`) and quarantine IDs (`Q-…`) are validated by
`planner.ValidID` and `NormalizeID`. Claude's plans are capped (5 open, 10 creation
calls per 10 minutes), enforced with a file lock so several MCP servers can't race past
the cap.
