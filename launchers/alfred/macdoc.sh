#!/bin/sh
# Alfred "Run Script" body (language /bin/sh, argument in $1: status or slow).
# Read-only: only `macdoc vitals` or `macdoc vitals --memory`.
PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export PATH
case "${1:-status}" in
  status) exec macdoc vitals ;;
  slow) exec macdoc vitals --memory ;;
  *) echo "usage: macdoc.sh status|slow" >&2; exit 2 ;;
esac
