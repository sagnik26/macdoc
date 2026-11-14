package tests

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/sandbox"
)

// syncBuf is a buffer a test can read while a command is still writing to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// sandboxFor puts the test under MACDOC_SANDBOX, as every apply test does.
func sandboxFor(t *testing.T, home string) {
	t.Helper()
	t.Setenv(sandbox.Env, filepath.Dir(home))
}

func termOn(t *testing.T, on bool) {
	t.Helper()
	old := cli.RealTTY
	cli.RealTTY = func(io.Reader, io.Writer) bool { return on }
	t.Cleanup(func() { cli.RealTTY = old })
}

// `apply --dry-run` seals and prints a plan, and changes nothing.
func TestApplyDryRunSealsButChangesNothing(t *testing.T) {
	home := cliFixture(t)
	sandboxFor(t, home)
	termOn(t, false) // a dry run needs no terminal
	out, err := run(t, "apply", "--tier", "caution", "--dry-run")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Plan P-", "Nothing has been changed", "To apply it: macdoc apply P-")
	if !present(filepath.Join(home, "code", "a", "node_modules")) {
		t.Error("a dry run removed something")
	}
	recs, err := engine.ListPlans(engine.OpportunityOptions{Home: home}, 5)
	if err != nil || len(recs) != 1 || recs[0].Status != "pending" {
		t.Errorf("plans = %+v, %v; want one pending plan", recs, err)
	}
}

// Without a real terminal, apply refuses before it seals anything.
func TestApplyWithoutATerminalSealsNothing(t *testing.T) {
	home := cliFixture(t)
	sandboxFor(t, home)
	termOn(t, false)
	out, err := run(t, "apply", "--safe")
	if err == nil || !strings.Contains(err.Error(), "Terminal") {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if recs, _ := engine.ListPlans(engine.OpportunityOptions{Home: home}, 5); len(recs) != 0 {
		t.Errorf("a plan was sealed without a terminal: %+v", recs)
	}
	if _, err := run(t, "apply", "--safe", "--json"); err == nil {
		t.Error("applying has no --json mode")
	}
}

// `apply` with nothing chosen applies nothing and says how to choose.
func TestApplyWithNothingChosenAppliesNothing(t *testing.T) {
	home := cliFixture(t)
	sandboxFor(t, home)
	termOn(t, true)
	out, err := run(t, "apply")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Nothing chosen, so nothing was changed.", "macdoc reclaim", "macdoc apply --safe")
	if !present(filepath.Join(home, "code", "a", "node_modules")) {
		t.Error("apply with no arguments removed something")
	}
}

// The whole merged flow in a sandbox: pick suggestions, see the plan, type
// its code, and it is applied and recorded. The code is only known once the
// plan is sealed, so a helper types it when the prompt appears.
func TestApplySelectionRunsTheWholeFlow(t *testing.T) {
	home := cliFixture(t)
	sandboxFor(t, home)
	termOn(t, true)
	cli.Runner = &platform.Fake{Responses: map[string]platform.Result{"ps -axww -o command=": {Stdout: []byte("/sbin/launchd\n")}}}

	pr, pw := io.Pipe()
	var out syncBuf
	cmd := cli.NewRootCmd()
	cmd.SetIn(pr)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"apply", "--recipe", "node.nm"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s := out.String()
		if i := strings.Index(s, `Type "`); i >= 0 {
			code, _, _ := strings.Cut(s[i+len(`Type "`):], `"`)
			_, _ = pw.Write([]byte(code + "\n"))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	mustContain(t, out.String(), "Plan P-", "applied: 5 done", "macdoc log")
	for _, p := range []string{"a", "b", "c", "d", "e"} {
		if present(filepath.Join(home, "code", p, "node_modules")) {
			t.Errorf("code/%s/node_modules is still there after apply", p)
		}
	}
}
