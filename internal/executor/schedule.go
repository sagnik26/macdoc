package executor

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sagnik26/macdoc/internal/sandbox"
)

// ScheduleLabel names macdoc's launch agent, the daily read-only scan.
const ScheduleLabel = "com.macdoc.scan"

// ScheduleHour is when the daily scan runs (local time).
const ScheduleHour = 9

// ScheduleAgentPath is the one file `schedule enable` writes and `schedule
// disable` removes (AGENTS invariant 1, Phase 10).
func ScheduleAgentPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", ScheduleLabel+".plist")
}

// schedulePlist is the launch agent: `macdoc scan --quiet` once a day, at low
// priority, with its output in macdoc's state folder. It changes nothing
// but macdoc's own scan record.
func schedulePlist(binary, logPath string) []byte {
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>scan</string>
		<string>--quiet</string>
	</array>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Hour</key>
		<integer>%d</integer>
		<key>Minute</key>
		<integer>0</integer>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>LowPriorityIO</key>
	<true/>
	<key>Nice</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, ScheduleLabel, esc(binary), ScheduleHour, esc(logPath), esc(logPath)))
}

// isMacdocAgent reports whether b is macdoc's launch agent (its label).
func isMacdocAgent(b []byte) bool {
	return bytes.Contains(b, []byte("<string>"+ScheduleLabel+"</string>"))
}

// InstallSchedule writes the daily scan agent for the given macdoc binary.
// It writes that one file. It doesn't load it: launchctl changes what runs,
// so the person runs the printed command in Terminal. An existing file that
// isn't macdoc's is never replaced.
func InstallSchedule(home, binary, logPath string) (string, error) {
	path := ScheduleAgentPath(home)
	dir := filepath.Dir(path)
	if err := sandbox.Check(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: the LaunchAgents folder, owner-managed.
		return "", err
	}
	if old, err := os.ReadFile(path); err == nil && !isMacdocAgent(old) { //nolint:gosec // G304: the agent path macdoc owns.
		return "", fmt.Errorf("%s exists and isn't macdoc's; it isn't replaced", path)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.WriteFile(path, schedulePlist(binary, logPath), 0o644); err != nil { //nolint:gosec // G306: a launch agent is read by launchd, not private.
		return "", err
	}
	return path, nil
}

// RemoveSchedule deletes macdoc's agent file, if there is one. It returns
// whether a file was removed. A file that isn't macdoc's is left alone.
func RemoveSchedule(home string) (bool, error) {
	path := ScheduleAgentPath(home)
	b, err := os.ReadFile(path) //nolint:gosec // G304: the agent path macdoc owns.
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !isMacdocAgent(b) {
		return false, fmt.Errorf("%s isn't macdoc's; it's left alone", path)
	}
	if err := sandbox.Check(filepath.Dir(path)); err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}
