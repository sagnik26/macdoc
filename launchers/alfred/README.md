# Alfred launcher

1. In an Alfred workflow add a Keyword input (for example `macdoc`) with an argument.
2. Connect a Run Script action: language `/bin/sh`, with argv, script `/path/to/launchers/alfred/macdoc.sh "$1"`.
3. Connect a Large Type or Copy to Clipboard output.

The script only runs `macdoc vitals` (`status` or `vitals`) or `macdoc vitals --memory` (`slow` or `memory`), both read-only. It never plans, applies or deletes. `status` and `slow` are the original keywords and still work.

Raycast: add `launchers/raycast` as a script-commands directory (`macdoc-status.sh`, `macdoc-slow.sh`).
