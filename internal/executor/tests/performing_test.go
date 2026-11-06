package tests

import (
	"context"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
)

// Once the apply is canceled, the items still to come are skipped and
// audited; the one already done stays done.
func TestInterruptedApplySkipsTheRest(t *testing.T) { //nolint:gocyclo // A linear list of assertions about one scenario.
	e := newEnv(t)
	a := e.folder(t, "Library/Logs/A")
	b := e.folder(t, "Library/Logs/B")
	c := e.folder(t, "Library/Logs/C")
	p := e.plan(t,
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, a),
		e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, b),
		e.item(t, "O-cccccc", "user.logs", model.RiskSafe, c))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.cfg.OnItem = func(ev executor.ItemEvent) {
		if ev.Result != nil && ev.Index == 1 {
			cancel()
		}
	}
	rep, err := executor.Apply(ctx, e.cfg, p, all(p))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Items[0].Outcome != executor.OutcomeDone || exists(a) {
		t.Fatalf("first item = %+v", rep.Items[0])
	}
	for _, it := range rep.Items[1:] {
		if it.Outcome != executor.OutcomeSkipped || it.Reason != "apply was interrupted" || it.QuarantineID != "" {
			t.Errorf("item = %+v", it)
		}
	}
	if !exists(b) || !exists(c) {
		t.Fatal("an item was moved after the apply was interrupted")
	}
	ev := e.events(t)
	if ev[2].Event != audit.ItemSkipped || ev[2].Reason != "apply was interrupted" || ev[3].Event != audit.ItemSkipped {
		t.Errorf("audit = %v", eventNames(ev))
	}
	if fin := ev[len(ev)-1]; fin.Result != "partial" {
		t.Errorf("finish = %+v", fin)
	}
}

// A precondition macdoc has no check for stops the item: it can't vouch for it.
func TestUnknownPreconditionSkips(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	it := e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target)
	it.Preconditions = append(it.Preconditions, "moon_is_full")
	res := mustApply(t, e, e.plan(t, it)).Items[0]
	if res.Outcome != executor.OutcomeSkipped || res.Reason != "it has a precondition macdoc can't check" || !exists(target) {
		t.Fatalf("item = %+v", res)
	}
	if ev := e.events(t); ev[1].Event != audit.ItemSkipped || !strings.Contains(ev[1].Reason, "can't check") {
		t.Errorf("audit = %+v", ev[1])
	}
}
