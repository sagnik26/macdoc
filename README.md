<div align="center">
  <h1>macdoc</h1>
  <p><b>Find out why your Mac is full and slow, and get the space back only after you say so.</b></p>
  <a href="https://github.com/sagnik26/macdoc/stargazers"><img src="https://img.shields.io/github/stars/sagnik26/macdoc?style=flat-square" alt="Stars"></a>
  <a href="https://github.com/sagnik26/macdoc/releases"><img src="https://img.shields.io/github/v/release/sagnik26/macdoc?label=version&style=flat-square" alt="Version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg?style=flat-square" alt="License"></a>
  <a href="https://github.com/sagnik26/macdoc/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/sagnik26/macdoc/ci.yml?branch=main&label=CI&style=flat-square" alt="CI"></a>
  <a href="https://github.com/sagnik26/macdoc/commits"><img src="https://img.shields.io/github/commit-activity/m/sagnik26/macdoc?style=flat-square" alt="Commits"></a>
</div>

macdoc is a local CLI for developer Macs. It shows where the disk and the memory went, and it removes something only after a person confirms it in Terminal.

- **No network of its own.** The macdoc code makes no network calls and sends no telemetry. Tools it runs, such as `brew` or `docker`, may use the network for their own work. See [PRIVACY.md](PRIVACY.md).
- **Asks before removing.** There is no `--yes`. A removal happens only at a real Terminal, after you confirm it.
- **Agents propose, you apply.** `macdoc mcp` lets an assistant diagnose and propose a plan. It cannot apply, delete, move, or take a path. You run `macdoc apply` yourself.

## Install

macdoc supports Intel and Apple Silicon Macs.

```bash
brew install sagnik26/macdoc/macdoc
macdoc
```

Without Homebrew, the install script checks the download's sha256, installs to `~/.local/bin`, and never uses `sudo`:

```bash
curl -fsSL https://raw.githubusercontent.com/sagnik26/macdoc/main/install.sh | sh
```

macdoc is only distributed through Homebrew, this repository, and its [GitHub releases](https://github.com/sagnik26/macdoc/releases).

<details>
<summary><strong>A pinned release, a source build, shell completion</strong></summary>

`MACDOC_VERSION` selects a tag from the [releases page](https://github.com/sagnik26/macdoc/releases). `MACDOC_PREFIX` installs somewhere other than `~/.local`:

```bash
curl -fsSL https://raw.githubusercontent.com/sagnik26/macdoc/main/install.sh | MACDOC_VERSION=v0.1.0 MACDOC_PREFIX="$HOME/.local" sh
```

Building from source needs Go 1.27.4 or newer:

```bash
git clone https://github.com/sagnik26/macdoc.git && cd macdoc
make install               # installs into ~/.local/bin (BINDIR=/usr/local/bin to change)
```

Shell completion:

```bash
mkdir -p ~/.zfunc && macdoc completion zsh > ~/.zfunc/_macdoc
# then in ~/.zshrc, before compinit: fpath=(~/.zfunc $fpath)
```

`bash` and `fish` work the same way. macdoc is not notarized yet: if macOS blocks a downloaded binary, run `xattr -d com.apple.quarantine ~/.local/bin/macdoc`.

</details>

## Demo

The read-only recording is the welcome screen, `macdoc reclaim --dry-run` (the suggestion list, with no cleanup), and `macdoc vitals`. The tape is [docs/demo.tape](docs/demo.tape). On a Mac with [VHS](https://github.com/charmbracelet/vhs) installed:

```bash
make demo
```

That writes `docs/demo.gif`. The GIF is not in the repository until someone renders the tape on a Mac, so this page does not embed it.

## Features

- One terminal UI for the overview, cleanup suggestions, toolchains, projects, and where the space is
- Suggestions from a fixed recipe catalog, each with an honest size and what it costs to get back
- Installed apps and the data an uninstalled app left behind, never Apple's own apps or Mac App Store apps
- A folder view, what grew since an earlier scan, and idle project build folders, all from the last scan
- Maintenance steps (flush DNS, free inactive memory, reset Quick Look) that go through the system password prompt
- Live CPU, memory, disk, network, and battery, with a health score from fixed rules it shows
- An audit log (`macdoc log`) and a quarantine you can restore while the entry is still there

## Commands

| Command | What it answers |
|---|---|
| `macdoc` | Dashboard in a terminal; a one-screen summary when piped, or with `--json` or `--quiet` |
| `macdoc reclaim` | What can be cleaned. At a Terminal you tick risk types and press Enter on Clean |
| `macdoc retire` | Installed software and the data that belongs to it |
| `macdoc refresh` | Maintenance steps you can apply |
| `macdoc explore` | Where the space is, what grew, and idle project build folders |
| `macdoc vitals` | Live CPU, memory, disk, network, battery, and a health score |
| `macdoc scan` | A fresh scan or the last data, then the full report |
| `macdoc apply` | Seal a plan, show it, and remove it after you confirm |

`macdoc --help` also lists `log`, `quarantine`, `config`, `schedule`, `mcp`, `completion`, and `version`. Every command supports `--json`; the shapes are frozen in [`schema/`](schema/).

Flags, examples, configuration, and the Raycast and Alfred launchers are in [docs/COMMANDS.md](docs/COMMANDS.md).

## Safety in 30 seconds

macdoc can remove files. It checks the path, refuses protected places, and asks at a real Terminal before it removes anything.

- `retire`, `refresh`, `explore`, `vitals`, and `scan` only read. `reclaim` only lists unless you are at a real Terminal, tick risk types, and press Enter on the Clean button. `apply --dry-run` seals a plan and changes nothing.
- Plans and tools take suggestion IDs, never paths. There is no `--yes`.
- Every action comes from a fixed recipe with an exact command list: no shell, no `docker system prune`, no `--volumes`.
- Each item is checked again immediately before it is touched. Anything that changed is skipped and recorded.
- Folders go to a private quarantine and are deleted through the same checks. `macdoc quarantine restore` puts one back while it is there.
- System folders, `~/Documents`, `~/Desktop`, iCloud, Mail, Keychains, `.ssh`, `.git`, `.env*`, and database files are never touched. The one exception is an app bundle directly in `/Applications`, and that has its own confirmation.
- Administrator steps run only through the system password prompt, from an exact list, and never from a plan an assistant proposed.
- Every change is in `macdoc log`.

The boundaries and the tests that guard them are in [SECURITY.md](SECURITY.md), [docs/SAFETY_MODEL.md](docs/SAFETY_MODEL.md), and [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md).

## With an assistant

```bash
claude mcp add macdoc -- macdoc mcp
```

Or give the assistant [llms.txt](https://raw.githubusercontent.com/sagnik26/macdoc/main/llms.txt) or the skill in [skills/macdoc/SKILL.md](skills/macdoc/SKILL.md). It may run the read-only commands and `macdoc apply --dry-run`. Applying stays in your Terminal.

## Configuration

macdoc runs without a config file. Settings can only make it stricter or change timings. `macdoc config` shows them. Timeouts and Full Disk Access are in [docs/COMMANDS.md](docs/COMMANDS.md#configuration).

## Contributing

Rules for people are in [CONTRIBUTING.md](CONTRIBUTING.md). AI coding agents should read [AGENTS.md](AGENTS.md) first, because macdoc deletes files on people's Macs. Thanks to everyone who helped: the [contributors](https://github.com/sagnik26/macdoc/graphs/contributors).

## Support

- If macdoc helped you, give it a star and [share it](https://github.com/sagnik26/macdoc)
- Open an [issue](https://github.com/sagnik26/macdoc/issues) for anything that looks wrong or unsafe
- For a security problem, follow [SECURITY.md](SECURITY.md) instead of opening a public issue
- Product requirements are in [docs/PRD.md](docs/PRD.md). Design notes, the recipe catalog, and the decision records are under [docs/](docs/ARCHITECTURE.md)

## License

macdoc is open source under the MIT license; see [LICENSE](LICENSE).
