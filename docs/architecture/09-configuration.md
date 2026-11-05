# Configuration

Part of the [macdoc architecture guide](../../README.md#architecture).

## 11. Configuration

`internal/config` reads and writes `config.json` in macdoc's state folder:

- `plan_minutes` (5–120, default 30): how long a sealed plan stays valid.
- `native_timeout_minutes` (1–60, default 15): the limit for a tool's own cleanup command.
- `protect`: extra folders macdoc must never touch. Entries can only add protection; the
  built-in list cannot be shortened, and `..` is rejected.

`config.Policy` feeds those folders into the safety policy used by the engine, the
planner and the executor. An unreadable or invalid file is an error at all three
places, so macdoc fails closed. There is no `edit` command, because opening an editor
would run a program outside the executor.
