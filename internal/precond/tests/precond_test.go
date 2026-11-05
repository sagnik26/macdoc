package tests

import (
	"context"
	"errors"
	"macdoc/internal/precond"
	"strings"
	"testing"

	"macdoc/internal/model"
	"macdoc/internal/platform"
)

const psKey = "ps -axww -o command="

func check(t *testing.T, ps string, extra map[string]platform.Result, item model.PlanItem) error {
	t.Helper()
	r := &platform.FakeRunner{Responses: map[string]platform.Result{psKey: {Stdout: []byte(ps)}}}
	for k, v := range extra {
		r.Responses[k] = v
	}
	c := &precond.Checker{Runner: r, Home: t.TempDir()}
	return c.Check(context.Background(), item)
}

func TestProcessPreconditions(t *testing.T) {
	cases := []struct {
		pc, ps string
		fail   bool
	}{
		{"app_not_running:Xcode", "/Applications/Xcode.app/Contents/MacOS/Xcode\n", true},
		{"app_not_running:Xcode", "/Applications/Xcode-beta.app/Contents/MacOS/Xcode\n/usr/bin/xcodebuild\n", false},
		{"app_not_running:Android Studio", "/Applications/Android Studio.app/Contents/MacOS/studio\n", true},
		{"emulator_not_running", "/Users/me/Library/Android/sdk/emulator/qemu/darwin-aarch64/qemu-system-aarch64 -avd P\n", true},
		{"emulator_not_running", "/usr/bin/vim emulator.txt\n", false},
		{"simulator_not_running", "/Applications/Xcode.app/Contents/Developer/Applications/Simulator.app/Contents/MacOS/Simulator\n", true},
		{"simulator_not_running", "/sbin/launchd\n", false},
		{"gradle_daemons_stopped", "/usr/bin/java -cp x org.gradle.launcher.daemon.bootstrap.GradleDaemon 8.7\n", true},
		{"gradle_daemons_stopped", "/usr/bin/java -jar other.jar\n", false},
	}
	for _, c := range cases {
		err := check(t, c.ps, nil, model.PlanItem{Preconditions: []string{c.pc}})
		if (err != nil) != c.fail {
			t.Errorf("%s with %q = %v, want fail %v", c.pc, c.ps, err, c.fail)
		}
	}
}

func TestUncheckableFails(t *testing.T) {
	c := &precond.Checker{Runner: &platform.FakeRunner{Errors: map[string]error{psKey: errors.New("boom")}}}
	if err := c.Check(context.Background(), model.PlanItem{Preconditions: []string{"app_not_running:Xcode"}}); err == nil {
		t.Error("ps failure passed")
	}
	if err := (&precond.Checker{}).Check(context.Background(), model.PlanItem{Preconditions: []string{"docker_reachable"}}); err == nil {
		t.Error("no runner passed")
	}
	if err := check(t, "", nil, model.PlanItem{Preconditions: []string{"made_up"}}); err == nil {
		t.Error("unknown precondition passed")
	}
	if err := check(t, "", nil, model.PlanItem{}); err != nil {
		t.Errorf("no preconditions = %v", err)
	}
}

func TestDocker(t *testing.T) {
	key := "docker info --format {{.ServerVersion}}"
	if err := check(t, "", map[string]platform.Result{key: {Stdout: []byte("27.3.1\n")}}, model.PlanItem{Preconditions: []string{"docker_reachable"}}); err != nil {
		t.Errorf("reachable = %v", err)
	}
	if err := check(t, "", map[string]platform.Result{key: {ExitCode: 1}}, model.PlanItem{Preconditions: []string{"docker_reachable"}}); err == nil {
		t.Error("unreachable passed")
	}
}

func TestOllama(t *testing.T) {
	item := model.PlanItem{Preconditions: []string{"ollama_model_not_loaded"}, Argv: []string{"ollama", "rm", "llama3.2"}}
	hdr := "NAME    ID    SIZE    PROCESSOR    UNTIL\n"
	if err := check(t, "", map[string]platform.Result{"ollama ps": {Stdout: []byte(hdr)}}, item); err != nil {
		t.Errorf("idle = %v", err)
	}
	err := check(t, "", map[string]platform.Result{"ollama ps": {Stdout: []byte(hdr + "llama3.2:latest a 3 GB 100% GPU 4 minutes\n")}}, item)
	if err == nil || !strings.Contains(err.Error(), "loaded") {
		t.Errorf("loaded = %v", err)
	}
}

func TestKnown(t *testing.T) {
	if !precond.Known([]string{"app_not_running:Xcode", "git_clean"}) || precond.Known([]string{"app_not_running:"}) || precond.Known([]string{"nope"}) {
		t.Error("Known is wrong")
	}
}
