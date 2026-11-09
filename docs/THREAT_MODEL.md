# macdoc: threat model

This extends the short table in [SAFETY_MODEL.md](SAFETY_MODEL.md) §5. It names what is protected, who could attack it, where trust changes, and, for every defense, a test that fails if the defense is removed. `scripts/threats.sh` (run by `make check`) fails if a test named here no longer exists, so this document cannot quietly drift from the code.

A reference like `internal/mcp/tests.TestToolList` means the function `TestToolList` in `internal/mcp/tests/`.

## 1. What is protected

| Asset | Why it matters |
|---|---|
| The user's files, above all documents, keys and project work | macdoc is a tool that deletes; wrongly deleting is the worst outcome |
| The audit log and the plan seal | They are how a person checks what macdoc did and what they approved |
| The numbers macdoc shows | A person decides to delete based on them; a wrong number is a wrong decision |
| The user's terminal | Text from disk is shown there and could redraw what the person confirms |

## 2. Who could attack

| Adversary | Capability assumed |
|---|---|
| **Hostile content on disk** | Controls file and folder names, symlinks, hardlinks and repository contents in directories macdoc scans (a cloned repo, a downloaded archive) |
| **A prompt-injected Claude** | Can call the MCP tools with any arguments, and can be steered by text in names that the tools return |
| **Another local process** | Runs as the same user, can create, rename and replace files and symlinks while macdoc runs |
| **A careless user** | Approves quickly, runs `apply` on a stale plan, or edits a file by hand |

Out of scope: another user on the machine (macdoc runs unprivileged and never uses `sudo`), a compromised macOS or kernel, and an attacker who can already run commands as the user and could delete the files directly.

## 3. Trust boundaries

```
disk ──(1)──▶ scanner ──▶ store ──▶ engine ──(2)──▶ MCP / Claude
                                      │
                                      └──▶ planner ──(3)──▶ plan file ──(4)──▶ executor ──(5)──▶ disk
                                                                                  ▲
                                                                  person at a terminal (6)
```

1. Disk contents are untrusted input. 2. Everything sent to Claude is untrusted data and must not become instructions. 3. A plan holds opportunity IDs, never paths from a caller. 4. A plan file is untrusted until it passes `planner.Verify`. 5. The executor re-validates every item immediately before acting. 6. Only a person at a real terminal approves.

## 4. Threats and defenses

| # | Threat | Defense | Tests that fail without it |
|---|---|---|---|
| T1 | Claude asks to delete an arbitrary path | No MCP tool executes, moves, deletes or takes a path; the executor cannot be imported by `mcp` or `engine` | `internal/mcp/tests.TestToolList`, `scripts/imports.sh` (self-test in `tools/forbidcheck/check/tests.TestForbidcheckScript`) |
| T2 | A file name contains instructions aimed at Claude | Every string returned over MCP is cleaned and capped, and marked as untrusted data | `internal/textsafe/tests.TestForModelAndTerminal`, `internal/mcp/tests.TestToolDescriptionsWarnUntrusted` |
| T3 | A file name carries terminal escapes that redraw what the user confirms | Human output prints a cleaned display copy; planning and the executor use the data as found | `internal/cli/tests.TestRedteamFixedApplyPromptEscapes`, `internal/cli/tests.TestRedteamFixedPlanRenderEscapes` |
| T4 | A plan file is edited, or forged along with the store row | A SHA-256 seal in the file is checked against a second copy in the store; every item is re-derived from the catalog | `internal/planner/tests.TestEditedPlanIsRejected`, `internal/planner/tests.TestForgedPlans`, `internal/planner/tests.TestVerifyChecksEveryTieBetweenFileAndStore`, `internal/planner/tests.TestVerifyRefusesItemsThePlannerCouldNotHaveMade`, `internal/executor/tests.TestRedteamHeldForgedPlans` |
| T5 | A stale plan is applied | Plans expire (30 minutes) and refuse a scan older than 6 hours | `internal/planner/tests.TestVerifyExpiryBoundary`, `internal/planner/tests.TestBuildTimeBoundaries` |
| T6 | A symlink or folder is swapped in after planning | Lstat, canonical path and fingerprint are re-checked per item, and the move is done relative to an open directory | `internal/executor/tests.TestRedteamFixedAncestorSwapAfterOpen`, `internal/executor/tests.TestRedteamFixedAncestorSwapDuringMove`, `internal/executor/tests.TestRedteamHeldSymlinksInsideTarget`, `internal/safety/tests.TestRedteamSymlinkSwapAfterCanonicalize` |
| T7 | A hardlink points a folder's contents outside the home directory | Contents are checked before the move | `internal/executor/tests.TestRedteamHeldHardLinkedFile` |
| T8 | A protected path is reached by case, Unicode or ancestor tricks | Component-wise comparison with case and Unicode folding; ancestors of protected roots are protected; the reported rule is pinned | `internal/safety/tests.TestRedteamAPFSFoldAliases`, `internal/safety/tests.TestRedteamAncestorOfProtectedRoot`, `internal/safety/tests.TestIsProtectedReasons`, `internal/safety/tests.FuzzCheckTarget`, `internal/safety/tests.FuzzRedteamCheckTarget` |
| T9 | A native tool is run with injected arguments | Fixed argv from the recipe; a dynamic argument must match the tool's own listing; reserved words are refused | `internal/executor/tests.TestRedteamHeldNativeArgInjection`, `internal/recipes/tests.TestNativeArgValidation` |
| T10 | Something other than the executor deletes, moves or runs commands | A forbidden-call check fails the build for any such call outside `internal/executor`, and pins each call site there | `tools/forbidcheck/check/tests.TestForbidcheckScript`, `internal/executor/tests.TestMutatingCallSites` |
| T11 | A cleanup happens without a person | `apply` refuses without a real terminal and a typed code; RISKY items and every item of a Claude plan are confirmed one by one; there is no `--yes` | `internal/cli/tests.TestApplyNeedsATerminal`, `internal/cli/tests.TestApplyRiskyAsksPerItem`, `internal/mcp/tests.TestRedteamHeldPlanIsClaudeOrigin`, `internal/cli/tests.TestTUIRemoveEnterTwice` |
| T12 | Claude floods the user with plans | At most 5 open Claude plans and 10 creations per 10 minutes, counted under a lock shared by every server process; repeated IDs collapse | `internal/mcp/tests.TestPlanRateLimit`, `internal/mcp/tests.TestPlanRateLimitAcrossServers`, `internal/mcp/tests.TestRedteamFixedIDNormalization` |
| T13 | macdoc crashes mid-apply | Manifest-first quarantine, fsynced audit log, a plan can run at most once | `internal/executor/tests.TestCrashMidApply`, `internal/executor/tests.TestRedteamFixedPurgeCrashOrder` |
| T14 | A restore overwrites newer data, or a forged quarantine entry is restored | Restore refuses when anything exists at the destination; entries are validated | `internal/executor/tests.TestRestoreNeverOverwrites`, `internal/executor/tests.TestRedteamHeldForgedQuarantineEntries` |
| T15 | A project with uncommitted changes is cleaned | A `git_clean` precondition is checked immediately before acting | `internal/precond/tests.TestGitClean` |
| T16 | A configured protected folder is dropped | The settings file can only add protection; an unreadable file stops planning and apply | `internal/executor/tests.TestConfigProtectAtApply` |
| T17 | A symlink at the launch-agent path redirects macdoc's one permitted write to another file | The path is inspected without following links and opened with `O_NOFOLLOW`, so a link is refused even if it appears after the check; removal leaves a link alone | `internal/executor/tests.TestScheduleRefusesALinkToNothing`, `internal/executor/tests.TestScheduleRefusesALinkToAnotherAgent` |

## 5. Known gaps

Stated so nobody assumes more than is true.

- **Sizes can be wrong on APFS.** `st_blocks * 512` overstates reclaimable space when files are clones of each other (measured up to 3x in `docs/experiments/S1-apfs-private-size`). The risk is a wrong decision, not data loss. The fix needs a design decision and is not made yet.
- **Not every defense is equally well tested.** Mutation testing (`docs/experiments/S2-mutation-testing`) found tests missing in `precond` and `safety`, which are fixed; `planner` and `executor` still have surviving mutants, mostly in error paths of `Restore`, `Purge` and `Apply`. A surviving mutant there means a failure branch has no test asserting its behavior.
- **Path comparison is by string.** Protected paths are matched by folded components, and every part of macdoc now uses the one definition in `internal/pathfold`. APFS decides what "the same folder" means, and its folding may differ from the Go library's for some Unicode versions (not tested). Comparing device and inode identities would remove the assumption; this is planned, not done.
- **No outside review of this version.** The earlier red-team passes are recorded in `docs/checkpoints/`; the current tree has had none.
- **Intel and macOS versions other than 26 are untested.**
