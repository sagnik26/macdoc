package executor

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

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

// readAgent reads the agent file without following a symlink. exists is false
// when there is no file. A symlink is an error: writing through it would change
// the file it points at, which is not the one file this command may touch.
func readAgent(path string) (data []byte, exists bool, err error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, true, fmt.Errorf("%s is a symlink; it's left alone", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0) //nolint:gosec // G304: the agent path macdoc owns.
	if err != nil {
		return nil, true, err
	}
	defer f.Close() //nolint:errcheck // read-only handle
	data, err = io.ReadAll(io.LimitReader(f, maxAgent))
	return data, true, err
}

// maxAgent bounds how much of an existing agent file is read to see whose it is.
const maxAgent = 1 << 20

// InstallSchedule writes the daily scan agent for the given macdoc binary.
// It writes that one file. It doesn't load it: launchctl changes what runs,
// so the person runs the printed command in Terminal. An existing file that
// isn't macdoc's is never replaced, and neither is a symlink: the file is
// opened without following links, so one that appears after the check is
// refused too.
func InstallSchedule(home, binary, logPath string) (string, error) {
	path := ScheduleAgentPath(home)
	dir := filepath.Dir(path)
	if err := sandbox.Check(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: the LaunchAgents folder, owner-managed.
		return "", err
	}
	old, exists, err := readAgent(path)
	if err != nil {
		return "", err
	}
	if exists && !isMacdocAgent(old) {
		return "", fmt.Errorf("%s exists and isn't macdoc's; it isn't replaced", path)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644) //nolint:gosec // G302,G304: a launch agent is read by launchd, not private; the agent path macdoc owns.
	if err != nil {
		return "", err
	}
	if _, err := f.Write(schedulePlist(binary, logPath)); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// RemoveSchedule deletes macdoc's agent file, if there is one. It returns
// whether a file was removed. A file that isn't macdoc's, and a symlink, are
// left alone.
func RemoveSchedule(home string) (bool, error) {
	path := ScheduleAgentPath(home)
	if err := sandbox.Check(filepath.Dir(path)); err != nil {
		return false, err
	}
	b, exists, err := readAgent(path)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	if !isMacdocAgent(b) {
		return false, fmt.Errorf("%s isn't macdoc's; it's left alone", path)
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}
