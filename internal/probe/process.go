package probe

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sagnik26/macdoc/internal/platform"
)

// CmdPS lists resident memory (KB) and command path of every process.
// Fixed argv, no shell; it never signals or changes any process.
var CmdPS = platform.Command{Name: "ps", Args: []string{"-axo", "rss=,comm="}}

// Friendly names for known dev-heavy process groups.
const (
	groupDocker          = "Docker"
	groupAndroidEmulator = "Android Emulator"
	groupNode            = "Node.js"
	groupOllama          = "Ollama"
	groupXcode           = "Xcode"
	groupAndroidStudio   = "Android Studio"
	groupIOSSimulator    = "iOS Simulator"
	groupQEMU            = "Virtual machine (QEMU)"
	// Docker Desktop, OrbStack, Colima (vz) and UTM all run their VM as
	// Apple's Virtualization service, so the owner can't be told apart.
	groupVirtualization = "Virtual machine"
)

// helperMarkers end an app name in a helper process name. A marker counts
// only at the end of the name or before " (" or ":", so "Google Chrome
// Helper (Renderer)" and "Cursor Helper: shared-process" map to their app
// but "DockHelper" does not.
var helperMarkers = []string{" Helper", " Web Content"}

// ParsePS parses `ps -axo rss=,comm=` output and returns the top n groups by
// resident memory. rss is in KB. n <= 0 returns all groups. Lines that
// don't start with an RSS number are skipped; output with no valid line is
// an error.
func ParsePS(out []byte, n int) ([]ProcessGroup, error) {
	groups := make(map[string]*ProcessGroup)
	parsed := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		rssField, comm, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		comm = strings.TrimSpace(comm)
		if !ok || comm == "" {
			continue
		}
		kb, err := strconv.ParseInt(rssField, 10, 64)
		if err != nil || kb < 0 || kb > (1<<53)/1024 {
			continue
		}
		parsed++
		name := GroupName(comm)
		g := groups[name]
		if g == nil {
			g = &ProcessGroup{Name: name}
			groups[name] = g
		}
		g.RSSBytes += kb * 1024
		g.Count++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("probe: ps: %w", err)
	}
	if parsed == 0 {
		return nil, errors.New("probe: ps: no process lines")
	}

	list := make([]ProcessGroup, 0, len(groups))
	for _, g := range groups {
		list = append(list, *g)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].RSSBytes != list[j].RSSBytes {
			return list[i].RSSBytes > list[j].RSSBytes
		}
		return list[i].Name < list[j].Name
	})
	if n > 0 && n < len(list) {
		list = list[:n]
	}
	return list, nil
}

// GroupName maps a ps comm value (a full path or a bare name, possibly a
// process title with spaces) to the group it is counted under.
func GroupName(comm string) string {
	base := comm
	if strings.HasPrefix(comm, "/") {
		base = comm[strings.LastIndex(comm, "/")+1:]
	}
	if g, ok := devGroup(comm, base); ok {
		return g
	}
	if app := outerAppName(comm); app != "" {
		return stripHelper(app)
	}
	return stripHelper(base)
}

// devGroup returns the friendly group for known dev-heavy processes. It is
// checked before the .app rule because some of them live inside another
// bundle (Simulator.app is inside Xcode.app).
func devGroup(comm, base string) (string, bool) {
	inDocker := strings.Contains(strings.ToLower(comm), "docker")
	switch {
	case strings.HasPrefix(base, "com.docker."), base == "Docker Desktop", base == "vpnkit":
		return groupDocker, true
	case strings.HasPrefix(base, "qemu-system-") && inDocker:
		return groupDocker, true
	case strings.HasPrefix(base, "emulator"):
		return groupAndroidEmulator, true
	case strings.HasPrefix(base, "qemu-system-"):
		// The Android emulator runs qemu from the SDK's emulator directory;
		// any other qemu (Colima, Lima, UTM) is a generic VM.
		lc := strings.ToLower(comm)
		if strings.Contains(lc, "/emulator/") || strings.Contains(lc, "android") {
			return groupAndroidEmulator, true
		}
		return groupQEMU, true
	case strings.HasPrefix(base, "com.apple.Virtualization.VirtualMachine"):
		return groupVirtualization, true
	case base == "node":
		return groupNode, true
	case base == "ollama", base == "ollama_llama_server":
		return groupOllama, true
	case base == "Simulator", base == "launchd_sim":
		return groupIOSSimulator, true
	case base == "Xcode", base == "SourceKitService", base == "XCBBuildService":
		return groupXcode, true
	case (base == "java" || base == "studio") && strings.Contains(comm, "/Android Studio.app/"):
		return groupAndroidStudio, true
	}
	return "", false
}

// outerAppName returns X for the first "/X.app/" component of a path, or "".
func outerAppName(comm string) string {
	idx := strings.Index(comm, ".app/")
	if idx < 0 || !strings.HasPrefix(comm, "/") {
		return ""
	}
	start := strings.LastIndex(comm[:idx], "/") + 1
	return comm[start:idx]
}

// stripHelper removes a helper suffix ("Google Chrome Helper (GPU)" →
// "Google Chrome"). Names without a marker are returned unchanged.
func stripHelper(name string) string {
	for _, m := range helperMarkers {
		i := strings.Index(name, m)
		if i <= 0 {
			continue
		}
		rest := name[i+len(m):]
		if rest == "" || strings.HasPrefix(rest, " (") || strings.HasPrefix(rest, ":") {
			return name[:i]
		}
	}
	return name
}

// CollectProcesses runs ps through r and returns the top n groups.
func CollectProcesses(ctx context.Context, r platform.Runner, n int) ([]ProcessGroup, error) {
	out, err := RunReadOnly(ctx, r, CmdPS)
	if err != nil {
		return nil, err
	}
	return ParsePS(out, n)
}
