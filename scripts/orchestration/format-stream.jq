# Render claude --output-format stream-json events as readable pane output.
if .type == "system" and .subtype == "init" then
  "── session \(.session_id[0:8]) · model \(.model) · cwd \(.cwd)"
elif .type == "assistant" then
  (.message.content[]? |
    if .type == "text" then "\n💬 " + .text
    elif .type == "tool_use" then
      "▶ \(.name) " + ((.input.command // .input.file_path // .input.pattern // .input.description // "") | tostring | .[0:160])
    else empty end)
elif .type == "user" then
  (.message.content[]? | select(type == "object" and .type == "tool_result" and .is_error == true) |
    "  ✗ " + ((.content | if type == "array" then (map(.text? // "") | join(" ")) else tostring end) | .[0:200]))
elif .type == "result" then
  "\n══ WORKER FINISHED: \(.subtype) · turns \(.num_turns) · \((.duration_ms/60000*10|floor)/10) min · $\(.total_cost_usd // 0 | . * 100 | floor / 100)"
else empty end
