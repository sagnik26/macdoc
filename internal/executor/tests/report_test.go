package tests

import (
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
)

// The Report totals are the numbers a person reads after an apply and the
// numbers written to the audit log, so each one is pinned with values that
// make addition, subtraction, assignment and a dropped condition all differ.
// Mutation testing (plan.md claim 2) showed none of them was pinned before.

func ptr(n int64) *int64 { return &n }

func TestDefaultNativeTimeout(t *testing.T) {
	if executor.DefaultNativeTimeout != 15*time.Minute {
		t.Errorf("DefaultNativeTimeout = %v, want 15m", executor.DefaultNativeTimeout)
	}
}

func TestReportCount(t *testing.T) {
	r := executor.Report{Items: []executor.ItemResult{
		{Outcome: executor.OutcomeDone}, {Outcome: executor.OutcomeDone}, {Outcome: executor.OutcomeDone},
		{Outcome: executor.OutcomeSkipped}, {Outcome: executor.OutcomeSkipped},
		{Outcome: executor.OutcomeFailed},
	}}
	for o, want := range map[executor.Outcome]int{
		executor.OutcomeDone: 3, executor.OutcomeSkipped: 2, executor.OutcomeFailed: 1, "other": 0,
	} {
		if got := r.Count(o); got != want {
			t.Errorf("Count(%q) = %d, want %d", o, got, want)
		}
	}
	if got := (executor.Report{}).Count(executor.OutcomeDone); got != 0 {
		t.Errorf("Count on an empty report = %d", got)
	}
}

func TestReportMovedAndDeletedBytes(t *testing.T) {
	r := executor.Report{Items: []executor.ItemResult{
		{MovedBytes: 100},                // held in quarantine
		{MovedBytes: 20},                 // held in quarantine
		{MovedBytes: 7, Deleted: true},   // moved, then deleted at once
		{MovedBytes: 300, Deleted: true}, // moved, then deleted at once
		{},                               // nothing moved
	}}
	if got := r.MovedBytes(); got != 120 {
		t.Errorf("MovedBytes = %d, want 120 (deleted items are not held)", got)
	}
	if got := r.DeletedBytes(); got != 307 {
		t.Errorf("DeletedBytes = %d, want 307 (held items are not deleted)", got)
	}
	if got := (executor.Report{}).MovedBytes(); got != 0 {
		t.Errorf("empty MovedBytes = %d", got)
	}
}

func TestReportNativeExpected(t *testing.T) {
	native := func(outcome executor.Outcome, method model.Method, src model.Source, n int64) executor.ItemResult {
		return executor.ItemResult{
			Outcome: outcome,
			Item:    model.PlanItem{Method: method, BytesSource: src, ExpectedBytes: n},
		}
	}
	r := executor.Report{Items: []executor.ItemResult{
		native(executor.OutcomeDone, model.MethodNative, model.SourceMeasured, 1000),
		native(executor.OutcomeDone, model.MethodNative, model.SourceTool, 20),
		// Each of these misses exactly one condition and must not count.
		native(executor.OutcomeSkipped, model.MethodNative, model.SourceMeasured, 5),
		native(executor.OutcomeFailed, model.MethodNative, model.SourceMeasured, 50),
		native(executor.OutcomeDone, model.MethodQuarantine, model.SourceMeasured, 500),
		native(executor.OutcomeDone, model.MethodNative, model.SourceUnknown, 9000),
	}}
	if got := r.NativeExpected(); got != 1020 {
		t.Errorf("NativeExpected = %d, want 1020", got)
	}
}

func TestReportFreed(t *testing.T) {
	tests := []struct {
		name          string
		before, after *int64
		want          int64
		ok            bool
	}{
		{"freed space", ptr(1000), ptr(1750), 750, true},
		{"nothing freed", ptr(500), ptr(500), 0, true},
		{"less free than before", ptr(900), ptr(400), -500, true},
		{"before not measured", nil, ptr(10), 0, false},
		{"after not measured", ptr(10), nil, 0, false},
		{"neither measured", nil, nil, 0, false},
	}
	for _, tt := range tests {
		got, ok := executor.Report{FreeBefore: tt.before, FreeAfter: tt.after}.Freed()
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: Freed = %d, %v; want %d, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
