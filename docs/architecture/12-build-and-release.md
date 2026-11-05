# Build, CI and release

Part of the [macdoc architecture guide](../../README.md#architecture).

## 14. Build, CI and release

`Makefile` targets:

| Target | What it does |
|---|---|
| `build` | `go build -ldflags "-s -w -X …cli.version=…"` to `bin/macdoc` (symbols stripped, ~10 MB) |
| `install` | builds and copies to `~/.local/bin` (`BINDIR` to change) |
| `test` | `go test -race ./...` |
| `cover` | coverage across packages |
| `lint` | `golangci-lint run ./...` (standard set plus gosec, misspell, errorlint, gocritic and others) |
| `importcheck`, `forbidcheck` | the two guards |
| `fuzz` | the safety fuzz targets for 30 seconds each |
| `check` | `test`, `lint`, `importcheck`, `forbidcheck` |

`.github/workflows/ci.yml` runs test, importcheck, forbidcheck, build and the short
fuzz targets on each change. `macdoc version` prints the commit the binary was built
from.
