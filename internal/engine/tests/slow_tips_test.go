package tests

import (
	"macdoc/internal/engine"
	"strings"
	"testing"

	"macdoc/internal/probe"
)

// Tips come only for measured groups with a known tip, and name the size.
func TestTipsForMeasuredGroups(t *testing.T) {
	groups := []engine.SlowGroup{
		{ProcessGroup: probe.ProcessGroup{Name: "Docker", RSSBytes: 3 << 30, Count: 2}},
		{ProcessGroup: probe.ProcessGroup{Name: "Google Chrome", RSSBytes: 4 << 30}}, // no tip for browsers
		{ProcessGroup: probe.ProcessGroup{Name: "Ollama", RSSBytes: 0}},              // not measured: no tip
		{ProcessGroup: probe.ProcessGroup{Name: "Xcode", RSSBytes: 1 << 30}},
	}
	tips := engine.TipsFor(groups)
	if len(tips) != 2 {
		t.Fatalf("tips = %+v, want Docker and Xcode only", tips)
	}
	if tips[0].Group != "Docker" || !strings.Contains(tips[0].Text, "3.0 GB measured") {
		t.Errorf("docker tip = %+v", tips[0])
	}
	if tips[1].Group != "Xcode" || !strings.Contains(tips[1].Text, "1.0 GB measured") {
		t.Errorf("xcode tip = %+v", tips[1])
	}
}

// Suggestions are advice only: none names an action macdoc takes.
func TestTipsAreAdviceOnly(t *testing.T) {
	for g, text := range engine.SlowTips {
		for _, verb := range []string{"macdoc quits", "kill", "stop it for you"} {
			if strings.Contains(strings.ToLower(text), verb) {
				t.Errorf("tip for %s acts on the process: %q", g, text)
			}
		}
	}
}
