# Releasing macdoc

## Cutting a release

1. `main` is green: `make check` passes locally and CI is green on the commit.
2. Tag the commit and push the tag: `git tag v0.1.0 && git push origin v0.1.0`.
3. `.github/workflows/release.yml` runs on a macOS runner (the purgeable-space shim uses cgo, so this cannot be built on Linux). It repeats the tests and guards, runs `govulncheck`, builds with GoReleaser, attests the build provenance, and creates a **draft** GitHub release.
4. Review the draft: the changelog, the two archives (`darwin_arm64`, `darwin_amd64`) and `checksums.txt`. Publish it by hand.

Nothing is published automatically. A release still needs no secrets beyond the built-in `GITHUB_TOKEN`. Developer ID signing and notarization run only when the Apple secrets below are set; empty secrets leave the binaries unsigned, which is what ships today.

## Dry run

```sh
goreleaser check
goreleaser release --snapshot --clean --skip=publish   # writes dist/, publishes nothing
```

## Checking that a release is what the source says

Each archive is built with `-trimpath` and file times fixed to the commit, and one archive is made per architecture. On this repository, two local builds of the same commit produced byte-identical archives (checked 2026-11-06 with `shasum -a 256`), so anyone can rebuild a tag and compare:

```sh
git checkout v0.1.0
goreleaser release --clean --skip=publish,validate
shasum -a 256 dist/*.tar.gz        # compare with checksums.txt in the release
```

Also available: `gh attestation verify <archive> --repo sagnik26/macdoc` checks the provenance recorded by the workflow.

What has **not** been verified: that a build on GitHub's runner matches a build on a laptop. The Go version and Xcode command-line tools differ between machines and can change the output, so a mismatch there is possible and would not by itself mean tampering. Compare against a build with the same Go version (`go version` is in the release notes of the workflow run) before drawing a conclusion.

## Why not a universal binary

A universal (arm64 + x86_64) binary was tried first. Its two slices were byte-identical across builds, but GoReleaser's fat-file wrapper was not, so the universal file could not be verified by hash. Per-architecture archives can, so that is what ships.

## Developer ID signing and notarization

`.goreleaser.yaml` signs and notarizes the darwin binaries with GoReleaser's quill integration (`notarize.macos`) only when `MACOS_SIGN_P12` is set and non-empty. That is the open-source path for a bare binary. The Pro-only `notarize.macos_native` path (codesign, app bundles, DMGs) is not used: macdoc ships one binary per architecture in a tar.gz, not an app.

Until those secrets exist, a downloaded binary is still blocked by Gatekeeper until the person clears the quarantine attribute (`xattr -d com.apple.quarantine macdoc`).

What you need from Apple (an Apple Developer Program membership, paid yearly):

1. A **Developer ID Application** certificate. Import the `.cer` into Keychain, export it as a `.p12`, and note the export password.
2. An **App Store Connect API key** (a `.p8` file). Note the key's ID and the issuer ID shown when you create the key.

Base64-encode the two files (no line wraps) and store them as GitHub Actions secrets on this repository. Do not commit the files or the password.

| Secret | Value |
|---|---|
| `MACOS_SIGN_P12` | base64 of the `.p12` certificate |
| `MACOS_SIGN_PASSWORD` | password for that `.p12` |
| `MACOS_NOTARY_KEY` | base64 of the `.p8` API key |
| `MACOS_NOTARY_KEY_ID` | the key ID (also in the `.p8` filename) |
| `MACOS_NOTARY_ISSUER_ID` | the issuer UUID |

```sh
base64 < Certificates.p12 | tr -d '\n'
base64 < ApiKey_XXXXXXXXXX.p8 | tr -d '\n'
```

`.github/workflows/release.yml` already passes those secret names into the GoReleaser step. When a secret is missing, GitHub sets it to empty and GoReleaser skips the block (`isEnvSet` is false for an empty value), so a tag push still builds the unsigned archives.

Notarization is submitted and not waited on (`wait` is left at its default, false). Apple can take a while, and the release job's timeout is 30 minutes. A binary signed this way is notarized once Apple accepts it; Gatekeeper on a Mac that is online can check that. Stapling the ticket into the binary means setting `wait: true` under `notarize.macos[].notarize` (capped at 20 minutes by GoReleaser). Do that only after a release with the secrets has succeeded once.

## Not done yet

- A Homebrew tap: create the `homebrew-macdoc` repository yourself and copy `packaging/homebrew/macdoc.rb` into `Formula/`.
- A tagged release: v0.1.0 is published (its first build was blocked by govulncheck, GO-2026-6859, fixed by requiring Go 1.27.4, and the tag was moved to the fixed commit before any release existed).

## Install script and Homebrew

- `install.sh` downloads `macdoc_<version>_darwin_<arch>.tar.gz` and `checksums.txt` from the GitHub release, checks the sha256, and installs to `~/.local/bin` (no sudo). `MACDOC_VERSION=v0.1.0` picks a release; `MACDOC_PREFIX=DIR` installs to `DIR/bin`. It refuses on a mismatch. Checked offline on 2026-11-14 against a fake release (a stubbed `curl` serving a local archive): a good archive installed after the checksum matched, a tampered archive and a missing checksum line were both refused with nothing installed. Run against the published v0.1.0 on 2026-11-14 (pinned and latest): the checksum verified and `macdoc version` printed 0.1.0.
- `packaging/homebrew/macdoc.rb` is a template. After publishing a release, replace `VERSION` (no `v`), `SHA256_ARM64` and `SHA256_AMD64` (from `checksums.txt`), then commit it to the tap.
- Agent files: `llms.txt`, `skills/macdoc/SKILL.md`, `.claude-plugin/plugin.json`, `launchers/`.

## Finishing phase 9 by hand (owner steps)

Nothing below has been run. In order:

1. Commit the work and merge it, then `git tag v0.1.0 && git push origin v0.1.0` (the release workflow makes a draft release).
2. Publish the draft, then test the installer: `MACDOC_VERSION=v0.1.0 sh install.sh` and run `macdoc version`.
3. The `homebrew-macdoc` tap exists (`sagnik26/homebrew-macdoc`). For each release:  copy `packaging/homebrew/macdoc.rb` into `Formula/`, replace `VERSION` (no `v`), `SHA256_ARM64` and `SHA256_AMD64` from `checksums.txt`, and run `brew install --build-from-source ./Formula/macdoc.rb`.
