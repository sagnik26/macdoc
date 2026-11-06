package recipes

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

// Detectors for Xcode, the iOS simulators and CocoaPods (catalog/xcode.yaml).
// All of them are read-only: scan nodes, directory listings and listing
// commands through env.Runner.

func init() {
	Register("xcode_device_support", DetectDeviceSupport)
	Register("simctl_unavailable_devices", detectSimctlUnavailable)
	Register("simctl_runtimes", detectSimctlRuntimes)
	Register("orphaned_sim_runtimes", detectOrphanedSimRuntimes)
	Register("simctl_idle_devices", DetectSimctlIdle)
	RegisterLister("simctl_idle_devices", listSimDeviceIDs)
	Register("cocoapods_cache", detectCocoaPodsCache)
}

// ---- shared helpers (also used by detect_android.go) ----

// nativeArgRE is what a NativeArg may look like: simctl UUIDs, runtime
// identifiers and AVD names. No whitespace, "/", shell characters or a
// leading "-" (which a tool would read as an option).
var nativeArgRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// reservedNativeArgs are tool keywords that select more than one listed item
// (e.g. `xcrun simctl runtime delete all`) or change what the command does.
var reservedNativeArgs = []string{"all", "unavailable", "booted", "help", "version"}

// ValidNativeArg reports whether s is safe to pass as a dynamic argument of
// a native command: short, made of allowed characters, and not a word the
// tool treats as "everything" (all, booted, ...).
func ValidNativeArg(s string) bool {
	return len(s) <= 255 && nativeArgRE.MatchString(s) &&
		!slices.ContainsFunc(reservedNativeArgs, func(r string) bool { return strings.EqualFold(r, s) })
}

// ToolMissing reports whether a listing command failed only because the tool
// (or a sub-tool such as simctl) isn't installed. That is not an error.
func ToolMissing(res platform.Result, err error) bool {
	msg := ""
	switch {
	case err != nil:
		msg = err.Error()
	case res.ExitCode != 0:
		msg = string(res.Stderr) + string(res.Stdout)
	default:
		return false
	}
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "unable to find utility") || strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no developer tools")
}

// runListing runs a read-only listing command. ok is false (with a nil error)
// when the tool isn't installed.
func runListing(ctx context.Context, env Env, name string, args ...string) (out []byte, ok bool, err error) {
	if env.Runner == nil || !ToolInstalled(name) {
		return nil, false, nil
	}
	cmd := platform.Command{Name: name, Args: args}
	res, err := env.Runner.Run(ctx, cmd)
	if ToolMissing(res, err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", platform.Key(cmd), err)
	}
	if res.ExitCode != 0 {
		return nil, false, fmt.Errorf("%s: exit %d: %s", platform.Key(cmd), res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return res.Stdout, true, nil
}

// ScanNode returns the kept scan node at exactly path.
func ScanNode(env Env, path string) (model.Node, bool, error) {
	if env.Nodes == nil {
		return model.Node{}, false, nil
	}
	nodes, err := env.Nodes.Nodes(path, 1)
	if err != nil {
		return model.Node{}, false, err
	}
	for _, n := range nodes {
		if strings.EqualFold(n.Path, path) {
			return n, true, nil
		}
	}
	return model.Node{}, false, nil
}

// scanChildren returns the kept direct children of dir.
func scanChildren(env Env, dir string) ([]model.Node, error) {
	nodes, err := env.Nodes.Nodes(dir, 1)
	if err != nil {
		return nil, err
	}
	out := nodes[:0:0]
	for _, n := range nodes {
		if !strings.EqualFold(n.Path, dir) {
			out = append(out, n)
		}
	}
	return out, nil
}

// unscanned is the size of a target the detector found on disk but the scan
// didn't keep.
func unscanned() model.Bytes {
	return model.Bytes{Source: model.SourceUnknown, Reason: "not in the latest scan (under the keep threshold, or not scanned yet)"}
}

// Clock returns env.Now, or the wall clock if it isn't set.
func (env Env) Clock() time.Time {
	if env.Now.IsZero() {
		return time.Now()
	}
	return env.Now
}

// versionRE finds a dotted numeric version, e.g. "17.2" in "iPhone15,2 17.2 (21C62)".
var versionRE = regexp.MustCompile(`^\d+(\.\d+)*$`)

// ParseVersion returns the numeric components of a dotted version, or nil.
func ParseVersion(s string) []int {
	if !versionRE.MatchString(s) {
		return nil
	}
	var v []int
	for _, p := range strings.Split(s, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		v = append(v, n)
	}
	return v
}

// allButNewest returns the items whose version is lower than the highest
// one. Items without a version are never returned; every item with the
// highest version is kept.
func allButNewest[T any](items []T, version func(T) []int) []T {
	var newest []int
	for _, it := range items {
		if v := version(it); v != nil && slices.Compare(v, newest) > 0 {
			newest = v
		}
	}
	var out []T
	for _, it := range items {
		if v := version(it); v != nil && slices.Compare(v, newest) < 0 {
			out = append(out, it)
		}
	}
	return out
}

// ---- xcode.device_support ----

var deviceSupportDirs = []string{"iOS DeviceSupport", "watchOS DeviceSupport", "tvOS DeviceSupport"}

// deviceSupportVersion takes the first field that is a plain version:
// "17.2 (21C62)" and "iPhone15,2 17.2 (21C62)" both give 17.2.
func deviceSupportVersion(n model.Node) []int {
	for _, f := range strings.Fields(filepath.Base(n.Path)) {
		if v := ParseVersion(f); v != nil {
			return v
		}
	}
	return nil
}

// DetectDeviceSupport finds Xcode DeviceSupport folders for every OS version
// except the newest one in each platform folder.
func DetectDeviceSupport(_ context.Context, env Env, _ Recipe) ([]Finding, error) {
	if env.Nodes == nil {
		return nil, nil
	}
	var out []Finding
	for _, d := range deviceSupportDirs {
		kids, err := scanChildren(env, filepath.Join(env.Home, "Library/Developer/Xcode", d))
		if err != nil {
			return nil, err
		}
		for _, n := range allButNewest(kids, deviceSupportVersion) {
			f := nodeFinding(n)
			f.Title = strings.TrimSuffix(d, " DeviceSupport") + " device support " + filepath.Base(n.Path)
			out = append(out, f)
		}
	}
	return out, nil
}

// ---- simctl ----

// simDevice is one entry of `xcrun simctl list -j devices` (see
// testdata/w1_simctl_devices.json for the assumed shape).
type simDevice struct {
	UDID         string `json:"udid"`
	Name         string `json:"name"`
	State        string `json:"state"`
	IsAvailable  bool   `json:"isAvailable"`
	DataPathSize int64  `json:"dataPathSize"`
	runtime      string
}

// simDevices lists simulator devices. ok is false when simctl is missing.
func simDevices(ctx context.Context, env Env) (devs []simDevice, ok bool, err error) {
	out, ok, err := runListing(ctx, env, "xcrun", "simctl", "list", "-j", "devices")
	if !ok || err != nil {
		return nil, ok, err
	}
	var doc struct {
		Devices map[string][]simDevice `json:"devices"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, false, fmt.Errorf("xcrun simctl list -j devices: %w", err)
	}
	for rt, ds := range doc.Devices {
		for _, d := range ds {
			d.runtime = rt
			devs = append(devs, d)
		}
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].UDID < devs[j].UDID })
	return devs, true, nil
}

// RuntimeName turns "com.apple.CoreSimulator.SimRuntime.iOS-17-2" into "iOS 17.2".
func RuntimeName(id string) string {
	i := strings.LastIndex(id, ".")
	plat, ver, _ := strings.Cut(id[i+1:], "-")
	if ver == "" {
		return plat
	}
	return plat + " " + strings.ReplaceAll(ver, "-", ".")
}

// SimDeviceDir is the CoreSimulator folder of the simulator with this UDID.
func SimDeviceDir(env Env, udid string) string {
	return filepath.Join(env.Home, "Library/Developer/CoreSimulator/Devices", udid)
}

// detectSimctlUnavailable emits a single finding for all unavailable devices,
// because `xcrun simctl delete unavailable` removes them in one go.
func detectSimctlUnavailable(ctx context.Context, env Env, _ Recipe) ([]Finding, error) {
	devs, ok, err := simDevices(ctx, env)
	if !ok || err != nil {
		return nil, err
	}
	var names []string
	var total int64
	missing, fromTool := 0, 0
	sumOK := true
	for _, d := range devs {
		if d.IsAvailable {
			continue
		}
		names = append(names, d.Name+" ("+RuntimeName(d.runtime)+")")
		// The UDID is joined into a path to size the device, so it must look
		// like an identifier; otherwise the device counts as not measured.
		if !ValidNativeArg(d.UDID) {
			missing++
			continue
		}
		n, found, err := ScanNode(env, SimDeviceDir(env, d.UDID))
		var size int64
		switch {
		case err != nil:
			return nil, err
		case found:
			size = n.Reclaimable()
		case d.DataPathSize > 0:
			size = d.DataPathSize
			fromTool++
		default:
			missing++
			continue
		}
		var ok bool
		if total, ok = AddSize(total, size); !ok {
			sumOK = false
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	b := model.Bytes{Value: total, Source: model.SourceMeasured}
	switch {
	case !sumOK:
		b = implausibleSize("simctl or the scan")
	case missing == len(names):
		b = model.Bytes{Source: model.SourceUnknown, Reason: "the unavailable simulators are not in the latest scan"}
	case missing > 0:
		b.Source, b.Reason = model.SourceEstimated, fmt.Sprintf("lower bound: %d of %d simulators not measured", missing, len(names))
	case fromTool > 0:
		b.Source = model.SourceTool
	}
	return []Finding{{
		Title: fmt.Sprintf("%d unavailable simulator(s)", len(names)),
		Bytes: b,
		Note:  "Simulators: " + strings.Join(names, ", "),
	}}, nil
}

// listSimDeviceIDs lists the UDIDs that `xcrun simctl delete` takes: the
// available, shut-down devices (P6-R1 F8). Read-only, and no staleness.
func listSimDeviceIDs(ctx context.Context, env Env, _ Recipe) ([]string, error) {
	devs, ok, err := simDevices(ctx, env)
	if !ok || err != nil {
		return nil, err
	}
	var out []string
	for _, d := range devs {
		if d.IsAvailable && d.State != "Booted" && ValidNativeArg(d.UDID) {
			out = append(out, d.UDID)
		}
	}
	return out, nil
}

// DetectSimctlIdle emits available, shut-down devices whose data directory
// hasn't changed for r.StaleDays (default 90).
func DetectSimctlIdle(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Nodes == nil {
		return nil, nil
	}
	devs, ok, err := simDevices(ctx, env)
	if !ok || err != nil {
		return nil, err
	}
	days := r.StaleDays
	if days <= 0 {
		days = 90
	}
	var out []Finding
	for _, d := range devs {
		if !d.IsAvailable || d.State == "Booted" || !ValidNativeArg(d.UDID) {
			continue
		}
		n, found, err := ScanNode(env, SimDeviceDir(env, d.UDID))
		if err != nil {
			return nil, err
		}
		if !found || !Stale(env, n.MtimeMax, days) {
			continue
		}
		f := nodeFinding(n)
		f.NativeArg = d.UDID
		f.Title = "Simulator " + d.Name + " (" + RuntimeName(d.runtime) + ")"
		out = append(out, f)
	}
	return out, nil
}

// simRuntime is one value of `xcrun simctl runtime list -j` (see
// testdata/w1_simctl_runtime_list.json).
type simRuntime struct {
	Identifier        string `json:"identifier"`
	Version           string `json:"version"`
	Build             string `json:"build"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	Path              string `json:"path"`
	SizeBytes         int64  `json:"sizeBytes"`
	Deletable         bool   `json:"deletable"`
	LastUsedAt        string `json:"lastUsedAt"`
}

// detectSimctlRuntimes emits every deletable runtime. The newest per platform
// gets a note, since Xcode needs it to run that platform's simulators.
func detectSimctlRuntimes(ctx context.Context, env Env, _ Recipe) ([]Finding, error) {
	out, ok, err := runListing(ctx, env, "xcrun", "simctl", "runtime", "list", "-j")
	if !ok || err != nil {
		return nil, err
	}
	var doc map[string]simRuntime
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("xcrun simctl runtime list -j: %w", err)
	}
	ids := make([]string, 0, len(doc))
	for id := range doc {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	plat := func(rt simRuntime) string {
		p, _, _ := strings.Cut(RuntimeName(rt.RuntimeIdentifier), " ")
		return p
	}
	newest := map[string][]int{}
	for _, id := range ids {
		rt := doc[id]
		if v := ParseVersion(rt.Version); slices.Compare(v, newest[plat(rt)]) > 0 {
			newest[plat(rt)] = v
		}
	}

	var fs []Finding
	for _, id := range ids {
		rt := doc[id]
		// The identifier is used verbatim as the native argument, so it must
		// be the listing's own key and look like an identifier.
		if !rt.Deletable || rt.Identifier != id || !ValidNativeArg(id) {
			continue
		}
		f := Finding{
			Target:    rt.Path,
			NativeArg: id,
			Title:     strings.TrimSpace(plat(rt)+" "+rt.Version) + " simulator runtime",
			Bytes:     model.Bytes{Value: rt.SizeBytes, Source: model.SourceTool},
		}
		if rt.SizeBytes <= 0 {
			f.Bytes = model.Bytes{Source: model.SourceUnknown, Reason: "simctl did not report a size"}
		} else if rt.SizeBytes > MaxPlausibleBytes {
			f.Bytes = implausibleSize("simctl")
		}
		if t, err := time.Parse(time.RFC3339, rt.LastUsedAt); err == nil {
			f.LastUsed = t.UTC()
		}
		if v := ParseVersion(rt.Version); v != nil && slices.Equal(v, newest[plat(rt)]) {
			f.Note = "This is the newest installed " + plat(rt) + " runtime; Xcode needs one to run " + plat(rt) + " simulators."
		}
		fs = append(fs, f)
	}
	return fs, nil
}

// orphanedRuntimeDirs are system locations simulator runtimes leave behind.
// They are outside home, so the recipe is advise-only.
var orphanedRuntimeDirs = []struct{ path, title string }{
	{"/System/Library/AssetsV2/com_apple_MobileAsset_iOSSimulatorRuntime", "Leftover iOS simulator runtime downloads"},
	{"/Library/Developer/CoreSimulator/Caches/dyld", "Leftover simulator dyld caches"},
	{"/Library/Developer/CoreSimulator/Images", "Leftover simulator runtime images"},
}

// detectOrphanedSimRuntimes reports runtime data on disk when simctl is not
// usable (e.g. only the Command Line Tools are selected). When simctl works,
// xcode.sim_runtimes covers these, so it emits nothing.
func detectOrphanedSimRuntimes(ctx context.Context, env Env, _ Recipe) ([]Finding, error) {
	if env.Nodes == nil || env.Runner == nil {
		return nil, nil
	}
	if ToolInstalled("xcrun") {
		cmd := platform.Command{Name: "xcrun", Args: []string{"--find", "simctl"}}
		res, err := env.Runner.Run(ctx, cmd)
		switch {
		case ToolMissing(res, err):
			// simctl unavailable: report what's left on disk
		case err != nil:
			return nil, fmt.Errorf("%s: %w", platform.Key(cmd), err)
		case res.ExitCode == 0:
			return nil, nil
		default:
			return nil, fmt.Errorf("%s: exit %d: %s", platform.Key(cmd), res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		}
	}
	var out []Finding
	for _, d := range orphanedRuntimeDirs {
		n, found, err := ScanNode(env, d.path)
		if err != nil {
			return nil, err
		}
		if found && n.Bytes > 0 {
			f := nodeFinding(n)
			f.Title = d.title
			out = append(out, f)
		}
	}
	return out, nil
}

// ---- cocoapods.cache ----

// detectCocoaPodsCache is the path detector, but only when `pod` is
// installed: the cleanup runs `pod cache clean --all`.
func detectCocoaPodsCache(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	if !ToolInstalled("pod") {
		return nil, nil
	}
	return DetectPath(ctx, env, r)
}
