package tests

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
)

func reportOf(expected int64, before, after *int64) executor.Report {
	return executor.Report{
		PlanID: "P-1",
		Items: []executor.ItemResult{{
			Item:       model.PlanItem{OpportunityID: "O-1"},
			Outcome:    executor.OutcomeDone,
			MovedBytes: expected,
			Deleted:    true,
		}},
		FreeBefore: before,
		FreeAfter:  after,
	}
}

func render(t *testing.T, rep executor.Report, snapshots int) string {
	t.Helper()
	var b bytes.Buffer
	if err := cli.RenderApply(&b, rep, snapshots); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func i64(n int64) *int64 { return &n }

func TestApplyReportExplainsLessThanExpected(t *testing.T) {
	out := render(t, reportOf(1_000_000, i64(0), i64(400_000)), 2)
	for _, want := range []string{"Less than expected", "2 local Time Machine snapshot(s)", "share space"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "more can be freed") {
		t.Errorf("the 'more' sentence appeared when less was freed:\n%s", out)
	}
}

func TestApplyReportOmitsSnapshotsWhenThereAreNone(t *testing.T) {
	out := render(t, reportOf(1_000_000, i64(0), i64(400_000)), 0)
	if !strings.Contains(out, "Less than expected") || strings.Contains(out, "Time Machine") {
		t.Errorf("unexpected explanation:\n%s", out)
	}
}

func TestApplyReportExplainsMoreThanExpected(t *testing.T) {
	out := render(t, reportOf(1_000_000, i64(0), i64(3_000_000)), 0)
	if !strings.Contains(out, "more can be freed") || strings.Contains(out, "Less than expected") {
		t.Errorf("wrong explanation:\n%s", out)
	}
}

func TestApplyReportSaysNothingWhenItMatchesOrCannotBeMeasured(t *testing.T) {
	cases := map[string]executor.Report{
		"exact":      reportOf(1_000_000, i64(0), i64(1_000_000)),
		"unmeasured": reportOf(1_000_000, nil, nil),
	}
	for name, rep := range cases {
		out := render(t, rep, 3)
		if strings.Contains(out, "Less than expected") || strings.Contains(out, "more can be freed") {
			t.Errorf("%s: unexpected explanation:\n%s", name, out)
		}
	}
	if out := render(t, reportOf(1_000_000, nil, nil), 0); !strings.Contains(out, "couldn't be measured") {
		t.Errorf("an unmeasured change must say so, not show zero:\n%s", out)
	}
}
