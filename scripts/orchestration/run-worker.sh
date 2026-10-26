#!/usr/bin/env bash
# Run a headless worker Claude session in a task worktree, rendering its
# progress for a tmux pane. Usage:
#   run-worker.sh <task-id> <prompt>             start a new session
#   run-worker.sh <task-id> --continue <prompt>  resume the task's session
set -uo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
task="$1"; shift
cont=()
if [ "${1:-}" = "--continue" ]; then cont=(--continue); shift; fi
prompt="$1"
wt="$root/.worktrees/$task"
cd "$wt" || { echo "no worktree $wt"; exit 1; }
mkdir -p .task
echo "▶▶ worker $task · $(date '+%H:%M:%S') · ${cont[*]+continue}"
claude -p ${cont[@]+"${cont[@]}"} "$prompt" \
  --output-format stream-json --verbose \
  --permission-mode acceptEdits \
  --allowedTools "Read" "Edit" "Write" "Glob" "Grep" \
    "Bash(go:*)" "Bash(gofmt:*)" "Bash(make:*)" "Bash(golangci-lint:*)" \
    "Bash(git status:*)" "Bash(git diff:*)" "Bash(git add:*)" "Bash(git commit:*)" \
    "Bash(git log:*)" "Bash(git show:*)" "Bash(mkdir:*)" "Bash(ls:*)" \
  --disallowedTools "Bash(git push:*)" "Bash(git checkout:*)" "Bash(git merge:*)" \
    "Bash(git worktree:*)" "Bash(git reset:*)" "Bash(rm:*)" "Bash(sudo:*)" \
    "Bash(curl:*)" "Bash(wget:*)" "WebFetch" "WebSearch" "Agent" \
  | tee -a .task/stream.jsonl \
  | jq -r --unbuffered -f "$root/scripts/orchestration/format-stream.jq"
echo "▶▶ worker $task exited ($(date '+%H:%M:%S')) — tech lead will review"
exec bash   # keep the pane open for inspection
