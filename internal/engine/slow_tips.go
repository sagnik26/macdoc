package engine

import "fmt"

// SlowTip is a suggestion for a process group whose memory was measured. It
// says what the person can do; macdoc never acts on the process (Phase 10).
type SlowTip struct {
	Group    string `json:"group"`
	RSSBytes int64  `json:"rss_bytes"` // measured by ps, not estimated
	Text     string `json:"text"`
}

// SlowTips maps a probe group name to the suggestion. Only groups the scan
// measured get a tip, and the tip names the measured size.
var SlowTips = map[string]string{
	"Docker":           "Quit Docker Desktop when you're not using containers.",
	"Ollama":           "Use a smaller quantized model, or unload the one you aren't using (`ollama ps` shows what's loaded).",
	"Android Emulator": "Close the emulators you aren't using; each holds its RAM setting until it's closed.",
	"iOS Simulator":    "Shut down the simulators you aren't using; they keep running with the Simulator window closed.",
	"Xcode":            "Close the projects you aren't using; indexing is what grows.",
	"java":             "Stop an idle Gradle daemon with `./gradlew --stop` in its project.",
	"Virtual machine":  "Stop a virtual machine you aren't using; it keeps its memory until it's stopped.",
}

// TipsFor returns the suggestions for the groups in groups that have a tip
// and a measured size, in list order.
func TipsFor(groups []SlowGroup) []SlowTip {
	var out []SlowTip
	for _, g := range groups {
		text, ok := SlowTips[g.Name]
		if !ok || g.RSSBytes <= 0 {
			continue
		}
		out = append(out, SlowTip{Group: g.Name, RSSBytes: g.RSSBytes,
			Text: fmt.Sprintf("%s (%s measured)", text, BinaryBytes(g.RSSBytes))})
	}
	return out
}
