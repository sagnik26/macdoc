#!/usr/bin/env bash
# Live status pane for the macdoc build session (read-only).
cd "$(dirname "$0")/../.." || exit 1
while true; do
  clear
  printf 'macdoc build status · %s\n\n' "$(date '+%H:%M:%S')"
  printf 'main: %s\n\n' "$(git log -1 --format='%h %s' main 2>/dev/null)"
  printf 'Task branches:\n'
  git for-each-ref --format='  %(refname:short)  %(objectname:short)  %(subject)' refs/heads/task/ 2>/dev/null || true
  printf '\nWorktrees:\n'
  for wt in .worktrees/*/; do
    [ -d "$wt" ] || continue
    r="pending"; [ -f "$wt.task/REPORT.md" ] && r="REPORT ready"
    t="-"; [ -f "$wt.task/last-test.txt" ] && t="$(tail -1 "$wt.task/last-test.txt")"
    printf '  %-12s %-13s last test: %s\n' "$(basename "$wt")" "$r" "$t"
  done
  printf '\nMemory: %s\n' "$(memory_pressure 2>/dev/null | tail -1)"
  printf 'Swap:   %s\n' "$(sysctl -n vm.swapusage)"
  sleep 10
done
