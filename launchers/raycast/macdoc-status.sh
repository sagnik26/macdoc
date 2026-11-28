#!/bin/bash

# Required parameters:
# @raycast.schemaVersion 1
# @raycast.title macdoc vitals
# @raycast.mode fullOutput

# Optional parameters:
# @raycast.icon 🩺
# @raycast.packageName macdoc

# Read-only: shows what the Mac is doing now. Never plans, applies or deletes.
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
command -v macdoc >/dev/null 2>&1 || { echo "macdoc is not installed"; exit 1; }
macdoc vitals
