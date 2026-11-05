# Platform access

Part of the [macdoc architecture guide](../../README.md#architecture).

## 12. Platform access

Everything that reads from macOS goes through `platform.Runner`:

- The real runner executes read-only commands with a timeout and an explicit argv. It
  never uses a shell.
- `platform.FakeRunner` maps a command line to canned output, so tests of probes,
  preconditions and detectors use recorded golden output.
- `platform/darwin` holds the optional cgo shim for the purgeable-space API. A stub file
  is used when cgo is off, so the default build needs no C toolchain.
- `platform.ResolveTool` finds a bare tool name on `PATH` and returns the path it will
  run, so the path can be checked before use.
