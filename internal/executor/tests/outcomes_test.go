package tests

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/quarantine"
)

// Why a native item did not run, or did not succeed, in the words a person
// reads.
func TestNativeReasons(t *testing.T) {
	brew := func(e *env) model.Plan {
		return e.plan(t, e.native(t, "O-aaaaaa", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"))
	}
	t.Run("exit status", func(t *testing.T) {
		withTools(t, "brew")
		e := newEnv(t)
		e.exec.code = 3
		if it := mustApply(t, e, brew(e)).Items[0]; it.Outcome != executor.OutcomeFailed || it.Reason != "brew exited with status 3" {
			t.Fatalf("item = %+v", it)
		}
	})
	t.Run("could not run", func(t *testing.T) {
		withTools(t, "brew")
		e := newEnv(t)
		e.cfg.Exec = func(context.Context, []string, time.Duration) ([]byte, int, error) {
			return nil, -1, errors.New("boom")
		}
		it := mustApply(t, e, brew(e)).Items[0]
		if it.Outcome != executor.OutcomeFailed || it.Reason != "brew couldn't run: boom" {
			t.Fatalf("item = %+v", it)
		}
	})
	t.Run("tool gone", func(t *testing.T) {
		withTools(t)
		e := newEnv(t)
		it := mustApply(t, e, brew(e)).Items[0]
		if it.Outcome != executor.OutcomeSkipped || it.Reason != "brew isn't installed any more" {
			t.Fatalf("item = %+v", it)
		}
	})
	ollama := func(e *env) model.Plan {
		return e.plan(t, e.native(t, "O-aaaaaa", "ollama.model", model.RiskCaution, "ollama", "rm", "qwen2.5:7b"))
	}
	t.Run("no longer listed", func(t *testing.T) {
		withTools(t, "ollama")
		e := newEnv(t)
		e.runner.Responses["ollama list"] = platform.Result{Stdout: []byte("NAME ID SIZE MODIFIED\n")}
		e.runner.Responses["ollama ps"] = platform.Result{Stdout: []byte("NAME ID SIZE PROCESSOR UNTIL\n")}
		it := mustApply(t, e, ollama(e)).Items[0]
		if it.Outcome != executor.OutcomeSkipped || it.Reason != "qwen2.5:7b is no longer listed by ollama" || len(e.exec.calls) != 0 {
			t.Fatalf("item = %+v", it)
		}
	})
	t.Run("listing fails", func(t *testing.T) {
		withTools(t, "ollama")
		e := newEnv(t)
		e.runner.Errors = map[string]error{"ollama list": errors.New("daemon not running")}
		e.runner.Responses["ollama ps"] = platform.Result{Stdout: []byte("NAME ID SIZE PROCESSOR UNTIL\n")}
		it := mustApply(t, e, ollama(e)).Items[0]
		if it.Outcome != executor.OutcomeSkipped || !strings.HasPrefix(it.Reason, "couldn't confirm qwen2.5:7b is still there: ") || len(e.exec.calls) != 0 {
			t.Fatalf("item = %+v", it)
		}
	})
}

// A simulator's identifier must still be in the simulator tool's own listing
// (a listing built by a lister, not a detector).
func TestSimulatorRelisted(t *testing.T) {
	const udid = "A1B2C3D4-0000-1111-2222-333344445555"
	key := "xcrun simctl list -j devices"
	listing := func(state string) platform.Result {
		return platform.Result{Stdout: []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-17-2":[` +
			`{"udid":"` + udid + `","isAvailable":true,"state":"` + state + `","name":"iPhone"}]}}`)}
	}
	setup := func(t *testing.T) (*env, model.Plan) {
		withTools(t, "xcrun")
		e := newEnv(t)
		return e, e.plan(t, e.native(t, "O-aaaaaa", "xcode.sim_devices_idle", model.RiskCaution, "xcrun", "simctl", "delete", udid))
	}
	t.Run("listed", func(t *testing.T) {
		e, p := setup(t)
		e.runner.Responses[key] = listing("Shutdown")
		if it := mustApply(t, e, p).Items[0]; it.Outcome != executor.OutcomeDone || len(e.exec.calls) != 1 {
			t.Fatalf("item = %+v, ran %v", it, e.exec.calls)
		}
	})
	t.Run("booted now", func(t *testing.T) {
		e, p := setup(t)
		e.runner.Responses[key] = listing("Booted")
		it := mustApply(t, e, p).Items[0]
		if it.Outcome != executor.OutcomeSkipped || it.Reason != udid+" is no longer listed by xcrun" || len(e.exec.calls) != 0 {
			t.Fatalf("item = %+v", it)
		}
	})
	t.Run("listing fails", func(t *testing.T) {
		e, p := setup(t)
		e.runner.Errors = map[string]error{key: errors.New("CoreSimulatorService is gone")}
		it := mustApply(t, e, p).Items[0]
		if it.Outcome != executor.OutcomeSkipped || !strings.HasPrefix(it.Reason, "couldn't confirm "+udid+" is still there: ") || len(e.exec.calls) != 0 {
			t.Fatalf("item = %+v", it)
		}
	})
}

// Progress events count the items from 1 and name the total.
func TestProgressEvents(t *testing.T) {
	e := newEnv(t)
	p := e.plan(t,
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")),
		e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/B")))
	var got []string
	e.cfg.OnItem = func(ev executor.Event) {
		state := "start"
		if ev.Result != nil {
			state = "end:" + string(ev.Result.Outcome)
		}
		got = append(got, fmt.Sprintf("%d/%d %s %s", ev.Index, ev.Total, ev.Item.OpportunityID, state))
	}
	mustApply(t, e, p)
	want := []string{"1/2 O-aaaaaa start", "1/2 O-aaaaaa end:done", "2/2 O-bbbbbb start", "2/2 O-bbbbbb end:done"}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v", got)
	}
}

// An item's audit record carries what was actually moved, only when something
// was.
func TestItemAuditBytes(t *testing.T) {
	withTools(t, "brew")
	e := newEnv(t)
	tiny := e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/B"))
	tiny.ExpectedBytes = 1
	p := e.plan(t,
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")),
		tiny,
		e.native(t, "O-cccccc", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"))
	rep := mustApply(t, e, p)
	ev := e.events(t)
	for i, want := range []int64{8192, 1, -1} {
		rec := ev[1+i]
		if rec.Event != audit.Done {
			t.Fatalf("event %d = %s", i, rec.Event)
		}
		switch {
		case want < 0 && rec.Actual != nil:
			t.Errorf("native item recorded %d actual bytes", *rec.Actual)
		case want >= 0 && (rec.Actual == nil || *rec.Actual != want || rep.Items[i].MovedBytes != want):
			t.Errorf("item %d actual bytes = %v, want %d", i, rec.Actual, want)
		}
	}
}

// DeleteNow only purges an item that was moved: a native command or an item
// whose move was undone has nothing in quarantine to delete.
func TestDeleteNowOnlyPurgesWhatWasMoved(t *testing.T) { //nolint:gocognit // A linear list of assertions about one scenario.
	t.Run("native item", func(t *testing.T) {
		withTools(t, "brew")
		e := newEnv(t)
		e.cfg.DeleteNow = true
		rep := mustApply(t, e, e.plan(t, e.native(t, "O-aaaaaa", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all")))
		if it := rep.Items[0]; it.Outcome != executor.OutcomeDone || it.Deleted || len(rep.Warnings) != 0 {
			t.Fatalf("item = %+v, warnings %v", it, rep.Warnings)
		}
		if got := eventNames(e.events(t)); slices.Contains(got, audit.Purged) {
			t.Errorf("a purge was attempted: %v", got)
		}
	})
	t.Run("move undone", func(t *testing.T) {
		e := newEnv(t)
		e.cfg.DeleteNow = true
		target := e.folder(t, "Library/Logs/A")
		p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
		e.cfg.StageHook = func(stage string) {
			if stage != "moved" {
				return
			}
			// Another folder takes the place of what arrived in quarantine.
			root := quarantine.Root(e.storePath)
			ents, _ := os.ReadDir(root)
			for _, d := range ents {
				if quarantine.ValidID(d.Name()) {
					payload := filepath.Join(root, d.Name(), "payload", "A")
					mustRename(t, payload, payload+".real")
					if err := os.Mkdir(payload, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		rep := mustApply(t, e, p)
		it := rep.Items[0]
		if it.Outcome != executor.OutcomeFailed || it.Deleted || !strings.Contains(it.Reason, "replaced by something else") {
			t.Fatalf("item = %+v", it)
		}
		if len(rep.Warnings) != 0 {
			t.Errorf("warnings = %v", rep.Warnings)
		}
		if got := eventNames(e.events(t)); slices.Contains(got, audit.Purged) {
			t.Errorf("a purge was attempted: %v", got)
		}
	})
}
