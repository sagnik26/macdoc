#!/bin/bash

# Required parameters:
# @raycast.schemaVersion 1
# @raycast.title macdoc vitals memory
# @raycast.mode fullOutput

# Optional parameters:
# @raycast.icon 🩺
# @raycast.packageName macdoc

# Read-only: explains whether memory is slowing the Mac. Never plans, applies or deletes.
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
command -v macdoc >/dev/null 2>&1 || { echo "macdoc is not installed"; exit 1; }
macdoc vitals --memory
