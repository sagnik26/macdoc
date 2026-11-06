package tests

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/probe"

	"github.com/sagnik26/macdoc/internal/platform"
)

func TestParsePSGolden(t *testing.T) {
	all, err := probe.ParsePS(w1Fixture(t, "ps.txt"), 0)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]probe.ProcessGroup{}
	var total int64
	count := 0
	for _, g := range all {
		byName[g.Name] = g
		total += g.RSSBytes
		count += g.Count
	}
	// Every one of the 384 lines in the capture is a valid process line.
	if count != 384 {
		t.Errorf("process count = %d, want 384", count)
	}
	// Totals computed independently from the fixture (sum of rss column).
	for name, want := range map[string]probe.ProcessGroup{
		"claude":        {Name: "claude", RSSBytes: (380576 + 127200) * 1024, Count: 2},
		"Node.js":       {Name: "Node.js", RSSBytes: (7440 + 3520 + 3520) * 1024, Count: 3},
		"Docker":        {Name: "Docker", RSSBytes: 4032 * 1024, Count: 1},
		"Google Chrome": byName["Google Chrome"],
		"Cursor":        byName["Cursor"],
	} {
		if got := byName[name]; got != want {
			t.Errorf("%s = %+v, want %+v", name, got, want)
		}
	}
	// Helpers are folded into their apps, not listed on their own.
	for name := range byName {
		if strings.Contains(name, " Helper") || strings.HasPrefix(name, "/") {
			t.Errorf("ungrouped name %q", name)
		}
	}
	// 49 lines under /Applications/Google Chrome.app/ and 17 Cursor lines
	// (bundle paths plus bare "Cursor Helper…: title" names).
	if g := byName["Google Chrome"]; g.Count != 49 || g.RSSBytes != 1917976576 {
		t.Errorf("Chrome = %+v", g)
	}
	if g := byName["Cursor"]; g.Count != 17 || g.RSSBytes != 1076805632 {
		t.Errorf("Cursor = %+v", g)
	}
	// DockHelper lives in Dock.app, so the bundle rule groups it as "Dock".
	if _, ok := byName["DockHelper"]; ok {
		t.Error("DockHelper should be grouped under Dock")
	}
	// Process titles that aren't paths are kept whole.
	if _, ok := byName["npm exec @playwright/mcp@latest"]; !ok {
		t.Error("missing npm process title group")
	}

	top, err := probe.ParsePS(w1Fixture(t, "ps.txt"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(top, all[:3]) {
		t.Errorf("top 3 = %+v, want prefix of all", top)
	}
	if top[0].Name != "Google Chrome" || top[1].Name != "Cursor" || top[2].Name != "claude" {
		t.Errorf("top 3 order = %+v", top)
	}
	w1AssertSorted(t, all)
}

func w1AssertSorted(t *testing.T, gs []probe.ProcessGroup) {
	t.Helper()
	for i := 1; i < len(gs); i++ {
		a, b := gs[i-1], gs[i]
		if a.RSSBytes < b.RSSBytes || (a.RSSBytes == b.RSSBytes && a.Name >= b.Name) {
			t.Fatalf("not sorted at %d: %+v then %+v", i, a, b)
		}
	}
}

func TestParsePSGrouping(t *testing.T) {
	got, err := probe.ParsePS(w1Fixture(t, "w1_ps_spaces.txt"), -1)
	if err != nil {
		t.Fatal(err)
	}
	want := []probe.ProcessGroup{
		{Name: "Docker", RSSBytes: (900000 + 300000 + 50000 + 10000 + 4032) * 1024, Count: 5},
		{Name: "Android Studio", RSSBytes: (800000 + 300000) * 1024, Count: 2},
		{Name: "Android Emulator", RSSBytes: (700000 + 20000) * 1024, Count: 2},
		{Name: "Xcode", RSSBytes: (400000 + 200000 + 100000) * 1024, Count: 3},
		{Name: "Ollama", RSSBytes: (600000 + 20000) * 1024, Count: 2},
		{Name: "Node.js", RSSBytes: (250000 + 150000) * 1024, Count: 2},
		{Name: "Google Chrome", RSSBytes: (102400 + 51200 + 20480) * 1024, Count: 3},
		{Name: "iOS Simulator", RSSBytes: (120000 + 30000) * 1024, Count: 2},
		{Name: "java", RSSBytes: 50000 * 1024, Count: 1},
		{Name: "Slack", RSSBytes: (30000 + 10000) * 1024, Count: 2},
		{Name: "Safari", RSSBytes: (4000 + 6000) * 1024, Count: 2},
		{Name: "Cursor", RSSBytes: 8000 * 1024, Count: 1},
		{Name: "Dock", RSSBytes: 2048 * 1024, Count: 1},
		{Name: "another tool", RSSBytes: 1000 * 1024, Count: 1}, // tie: by name
		{Name: "my tool", RSSBytes: 1000 * 1024, Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
	top, _ := probe.ParsePS(w1Fixture(t, "w1_ps_spaces.txt"), 100)
	if len(top) != len(want) {
		t.Errorf("n larger than groups: got %d", len(top))
	}
}

func TestGroupName(t *testing.T) {
	for comm, want := range map[string]string{
		"Google Chrome Helper":            "Google Chrome",
		"Google Chrome Helper (Renderer)": "Google Chrome",
		"Code Helper (GPU)":               "Code",
		"Code Helper (Plugin)":            "Code",
		"Safari Web Content":              "Safari",
		"Helper":                          "Helper",
		" Helper":                         " Helper",
		"Foo Helperish":                   "Foo Helperish",
		"DockHelper":                      "DockHelper",
		"/usr/bin/Helper Tool":            "Helper Tool",
		"/opt/x/qemu-system-x86_64":       "Virtual machine (QEMU)",
		"/Users/a/Library/Android/sdk/emulator/qemu/darwin-aarch64/qemu-system-aarch64": "Android Emulator",
		"/opt/homebrew/bin/qemu-system-aarch64":                                         "Virtual machine (QEMU)",
		"/Users/a/.docker/bin/qemu-system-aarch64":                                      "Docker",
		"/Users/a/Library/Android/sdk/emulator/emulator64":                              "Android Emulator",
		"/usr/bin/studio":     "studio",
		"/usr/bin/java":       "java",
		"ollama":              "Ollama",
		"Xcode":               "Xcode",
		"Docker Desktop":      "Docker",
		"com.docker.hyperkit": "Docker",
		"relative/path/app":   "relative/path/app",
		"/Weird.app":          "Weird.app",
	} {
		if got := probe.GroupName(comm); got != want {
			t.Errorf("groupName(%q) = %q, want %q", comm, got, want)
		}
	}
}

func TestParsePSBad(t *testing.T) {
	for name, in := range map[string]string{
		"empty":   "",
		"garbage": string(w1Fixture(t, "w1_garbage.txt")),
		"no comm": "  123\n 456 \n",
		"huge":    "99999999999999999999 /bin/x\n",
		"neg":     "-5 /bin/x\n",
	} {
		_, err := probe.ParsePS([]byte(in), 0)
		if err == nil || !strings.HasPrefix(err.Error(), "probe: ps: ") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCollectProcesses(t *testing.T) {
	f := &platform.FakeRunner{Responses: map[string]platform.Result{
		"ps -axo rss=,comm=": w1OK(w1Fixture(t, "w1_ps_spaces.txt")),
	}}
	got, err := probe.CollectProcesses(context.Background(), f, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Docker" || got[1].Name != "Android Studio" {
		t.Fatalf("got %+v", got)
	}
	w1AssertCalls(t, f, platform.Command{Name: "ps", Args: []string{"-axo", "rss=,comm="}})
}

func TestCollectProcessesError(t *testing.T) {
	f := &platform.FakeRunner{Responses: map[string]platform.Result{
		"ps -axo rss=,comm=": {ExitCode: 1, Stderr: []byte("ps: bad option\n")},
	}}
	_, err := probe.CollectProcesses(context.Background(), f, 5)
	if err == nil || err.Error() != "probe: ps -axo rss=,comm=: exit status 1: ps: bad option" {
		t.Fatalf("err = %v", err)
	}

	f = &platform.FakeRunner{Errors: map[string]error{"ps -axo rss=,comm=": context.DeadlineExceeded}}
	_, err = probe.CollectProcesses(context.Background(), f, 5)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	w1AssertCalls(t, f, probe.CmdPS)
}

func TestVirtualizationGroup(t *testing.T) {
	out := []byte("3000000 /System/Library/Frameworks/Virtualization.framework/Versions/A/XPCServices/com.apple.Virtualization.VirtualMachine.xpc/Contents/MacOS/com.apple.Virtualization.VirtualMachine\n" +
		"1000 /Applications/OrbStack.app/Contents/MacOS/OrbStack\n")
	got, err := probe.ParsePS(out, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Virtual machine" || got[0].RSSBytes != 3000000*1024 || got[1].Name != "OrbStack" {
		t.Errorf("got %+v", got)
	}
}
