#!/usr/bin/env bash
# Run a headless worker Claude session in a task worktree, rendering its
# progress for a tmux pane. Set EXTRA_ALLOW to a ";"-separated list of
# extra read-only tool rules, e.g. "Bash(sysctl:*);Bash(diskutil info:*)". Usage:
#   run-worker.sh <task-id> <prompt>             start a new session
#   run-worker.sh <task-id> --continue <prompt>  resume the task's session
set -uo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
task="$1"; shift
cont=()
if [ "${1:-}" = "--continue" ]; then cont=(--continue); shift; fi
prompt="$1"
extra=()
[ -n "${EXTRA_ALLOW:-}" ] && IFS=";" read -r -a extra <<< "$EXTRA_ALLOW"
wt="$root/.worktrees/$task"
cd "$wt" || { echo "no worktree $wt"; exit 1; }
mkdir -p .task
echo "▶▶ worker $task · $(date '+%H:%M:%S') · ${cont[*]+continue}"
# Edit/Write are NOT allowlisted: acceptEdits auto-approves edits inside the
# worktree only; edits elsewhere would need approval, which headless refuses.
claude -p ${cont[@]+"${cont[@]}"} "$prompt" \
  --output-format stream-json --verbose \
  --permission-mode acceptEdits \
  --allowedTools "Read" "Glob" "Grep" \
    "Bash(go:*)" "Bash(gofmt:*)" "Bash(make:*)" "Bash(golangci-lint:*)" \
    "Bash(git status:*)" "Bash(git diff:*)" "Bash(git add:*)" "Bash(git commit:*)" \
    "Bash(git log:*)" "Bash(git show:*)" "Bash(mkdir:*)" "Bash(ls:*)" \
    ${extra[@]+"${extra[@]}"} \
  --disallowedTools "Bash(git push:*)" "Bash(git checkout:*)" "Bash(git merge:*)" \
    "Bash(git worktree:*)" "Bash(git reset:*)" "Bash(rm:*)" "Bash(sudo:*)" \
    "Bash(curl:*)" "Bash(wget:*)" "WebFetch" "WebSearch" "Agent" \
  | tee -a .task/stream.jsonl \
  | jq -r --unbuffered -f "$root/scripts/orchestration/format-stream.jq"
echo "▶▶ worker $task exited ($(date '+%H:%M:%S')) — tech lead will review"
exec bash   # keep the pane open for inspection
