# S6: can macdoc slim a universal app without breaking it?

**Question.** Phase 7b of `docs/plans/mole-gaps.md` (Mole's `mo slim`) would remove the Intel slice from universal apps. A copy-modify-resign flow (`lipo -thin arm64` on every Mach-O, then `codesign --force --deep --sign -`) is the obvious way. Does it keep an app verifying, passing Gatekeeper, and (where it matters) launching?

**Scope and safety.** Everything ran on copies made with `ditto` into a scratch directory. Nothing in `/Applications` was written to (`codesign --verify` on the original Rectangle still passed afterwards). No `macdoc apply` was run. Apps are universal `x86_64 arm64` unless noted. Tested 2026-11-14, macOS 26 (Darwin 25.6), Apple silicon.

## Subjects

| Subject | Signing (original) | Size before |
|---|---|---|
| `Hello.app`, built here with `clang -arch x86_64 -arch arm64`, signed `codesign --sign -` | ad hoc, no Team ID | 108 KB |
| Rectangle 1.x (`/Applications`) | Developer ID, notarized, hardened runtime, Sparkle 2.5.1 | 7.7 MB |
| OnyX (`/Applications`) | Developer ID, notarized, hardened runtime | 13.4 MB |
| Snip My (`/Applications`) | Mac App Store (Apple signing) | 28 MB |

No ad-hoc or unsigned universal app was installed, so case (a) uses the synthetic `Hello.app`. Checked with `codesign -dv --verbose=2` and `spctl --assess --type execute -vv` before touching anything: Rectangle and OnyX are "accepted, source=Notarized Developer ID", Snip My is "accepted, source=Mac App Store", `Hello.app` is "rejected" (expected for ad hoc).

## Commands

```sh
S=<scratch>; cd $S
ditto /Applications/Rectangle.app Rectangle.app        # same for OnyX, "Snip My" (as Snip.app)
# thin every executable Mach-O in the bundle (main binary, frameworks, helpers, XPC services)
find X.app -type f -perm +111 | while read f; do
  lipo -archs "$f" 2>/dev/null | grep -q x86_64 && lipo -thin arm64 "$f" -output "$f.t" && mv "$f.t" "$f"
done
# Flow A: resign
codesign --force --deep --sign - X.app
# Flow B: thin only, signature left alone
codesign --verify --deep --strict --verbose=2 X.app
spctl --assess --type execute -vv X.app
codesign -dv --verbose=4 X.app          # compare CDHash and TeamIdentifier with the original
```

Rectangle thinned 7 Mach-O files (app, `RectangleLauncher`, and Sparkle's `Sparkle`, `Autoupdate`, `Updater`, `Downloader.xpc`, `Installer.xpc`). OnyX thinned 1. Snip My thinned 3 (app, login helper, `libswift_Concurrency.dylib`).

## Results

| Flow | Subject | `codesign --verify --deep --strict` | `spctl --assess --type execute` | Signature afterwards | Size |
|---|---|---|---|---|---|
| A: thin + ad-hoc resign | Hello | valid | **rejected** (it was rejected before too) | adhoc, no Team ID | 108 to 60 KB |
| A | Rectangle | valid | **rejected** | adhoc, **Team ID `XSYZ3E4B7D` lost** | 7.7 to 5.6 MB |
| A | OnyX | valid | **rejected** | adhoc, **Team ID `T49MRBL8UL` lost** | 13.4 to 13.3 MB |
| B: thin only, no resign | Rectangle | valid, nested Sparkle code validated | **accepted, Notarized Developer ID** | unchanged; same Team ID; **CDHash identical to the original** | 7.7 to ~5.6 MB |
| B | OnyX | valid | **accepted, Notarized Developer ID** | unchanged | 13.4 to 13.3 MB |
| B | Snip My (App Store) | valid | **accepted, Mac App Store** | unchanged; `_MASReceipt/receipt` still present | thinned 3 files |
| B, with `com.apple.quarantine` set | Rectangle | | **accepted** | | |

Savings are small: OnyX saves about 50 KB because most of its weight is resources, not code.

Not tested: launching. Rectangle and OnyX are GUI apps that need Accessibility or full-disk access and write to the user's preferences, so `open -n -a` on a copy is not trivial or offline-clean. `Hello` was run directly and printed `hello` (exit 0). `lipo -archs` on the thinned Rectangle shows `arm64`, and `otool -L` shows its libraries still resolve to the same paths.

## What this shows

1. **Re-signing destroys the identity (Flow A).** Ad-hoc re-signing passes `--verify` but drops the Developer ID certificate, the Team ID and the notarization ticket. Gatekeeper then rejects the app (`spctl: rejected`), so a quarantined copy would be blocked on first launch. This is the flow the plan asked about, and it fails for Developer-ID apps.
2. **Consequences of a changed Team ID** (from the signing model, not all exercised here): app-group containers and keychain access groups are keyed on the Team ID, so shared data and stored credentials can become unreachable. Sparkle 2 checks the updater's and the update's signatures (Rectangle ships `SUPublicEDKey`), so a re-signed app can refuse its own updates. Hardened-runtime entitlements are dropped by `--force` unless re-passed with `--entitlements`. TCC grants (Accessibility, Full Disk Access) are tied to the code requirement and would be asked for again. Mac App Store apps carry a receipt and an Apple signature, so a re-signed one stops being a Store app.
3. **Thinning alone keeps the signature valid (Flow B).** Each slice of a universal binary carries its own code signature, and the bundle seal does not hash the fat wrapper. `lipo -thin arm64` leaves the arm64 slice, its CDHash, and `CodeResources` untouched. All three apps, including the App Store one, still verify and Gatekeeper still accepts them. This is a better route than copy-modify-resign, but it is not a proof the app still behaves.
4. **What Flow B would still cost.** It needs write access to the bundle (`/Applications` is a protected root, and App Management permission applies to others' apps). It cannot be undone except by reinstalling or restoring a full copy, which gives back the space. It breaks Rosetta use (an Intel-only plug-in or helper), Intel Macs if the disk is shared, and delta updates (Sparkle deltas are computed against the original binary; a full update restores the Intel slice anyway). The app's own integrity checks and a running app are further unknowns. It also is not a delete, move or native command from a recipe: it is a new kind of mutation on someone else's signed bundle.

## Conclusion

The copy-modify-resign flow **fails** for Developer-ID apps: after thinning and an ad-hoc re-sign the app verifies but Gatekeeper rejects it and the Team ID is gone. 7b as planned is dropped. Thinning without re-signing keeps signatures valid in these three samples, which is worth recording, but the saving is small and the risks above (no undo, protected root, updates, Rosetta, launch not verified) mean macdoc stays advice-only for slimming. See ADR 0010.

Scratch files were left under the session scratchpad, not in the repository.
