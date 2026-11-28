#!/bin/sh
# Alfred "Run Script" body (language /bin/sh, argument in $1).
# Read-only: only `macdoc vitals` or `macdoc vitals --memory`.
# status and slow are the original keywords; vitals and memory name the command.
PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export PATH
case "${1:-status}" in
  status|vitals) exec macdoc vitals ;;
  slow|memory) exec macdoc vitals --memory ;;
  *) echo "usage: macdoc.sh status|vitals|slow|memory" >&2; exit 2 ;;
esac
