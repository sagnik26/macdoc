package tests

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/model"
)

func privPlan() model.Plan {
	return model.Plan{ID: "P-ABCD", Origin: model.OriginCLI, Items: []model.PlanItem{
		{OpportunityID: "O-safeaa", Title: "Quick Look", Risk: model.RiskSafe, Method: model.MethodNative, BytesSource: model.SourceUnknown},
		{OpportunityID: "O-privaa", Title: "Flush the DNS cache", Risk: model.RiskCaution, Method: model.MethodNative,
			BytesSource: model.SourceUnknown, Privileged: true, Argv: []string{"dscacheutil", "-flushcache"}},
	}}
}

// A step that needs administrator rights is asked about on its own, even in a
// plan a person made, and a "no" leaves it out; the ordinary item is still a
// group approval.
func TestConfirmItemsAsksAboutPrivilegedStepsOnTheirOwn(t *testing.T) {
	var out bytes.Buffer
	got, err := cli.ConfirmItems(bufio.NewReader(strings.NewReader("y\n")), &out, cli.PlainTheme(), privPlan())
	if err != nil {
		t.Fatal(err)
	}
	if ap, ok := got["O-privaa"]; !ok || !ap.Individually {
		t.Errorf("a yes should approve the privileged step on its own: %+v", got)
	}
	if ap, ok := got["O-safeaa"]; !ok || ap.Individually {
		t.Errorf("the ordinary item should be a group approval: %+v", got)
	}
	got, err = cli.ConfirmItems(bufio.NewReader(strings.NewReader("n\n")), &out, cli.PlainTheme(), privPlan())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["O-privaa"]; ok {
		t.Error("a no must leave the privileged step out")
	}
}
