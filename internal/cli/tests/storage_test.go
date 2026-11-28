package tests

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
)

func TestStorageCommand(t *testing.T) {
	home := cliFixture(t)
	out, err := run(t, "reclaim", "--categories")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "macdoc reclaim categories · scan S-", "Reclaimable, by category", "package-caches", "18.0 GB+", "7 item(s)",
		"SAFE 3.0 GB · CAUTION 15.0 GB", `"System Data", decoded`, "Local Time Machine snapshots", "—  unknown (",
		" Notes\n", "macdoc reclaim --toolchains · nothing has been changed")
	if strings.Contains(out, "macdoc reclaim · macdoc reclaim ·") {
		t.Error("categories footer repeats reclaim and names no other command")
	}
	if strings.Contains(out, "Large items") {
		t.Error("large shown without --large")
	}

	out, err = run(t, "reclaim", "--categories", "--large")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Large items with no recipe (≥ 1 GB): advice only, review manually", "4.0 GB  "+filepath.Join(home, "big.iso"))

	out, err = run(t, "reclaim", "--categories", "--large", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Command string               `json:"command"`
		Data    engine.StorageResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.Command != "reclaim categories" || len(env.Data.Large) != 1 || len(env.Data.SystemData) == 0 || len(env.Data.Categories) != 1 {
		t.Errorf("envelope = %+v", env)
	}
}

func TestRenderStorage(t *testing.T) {
	res := engine.StorageResult{
		SystemData: []engine.SystemDataPart{
			{Name: "VM", Bytes: model.Bytes{Value: 3 * gb, Source: model.SourceTool}, Explain: "swap"},
			{Name: "Hidden", Bytes: model.Bytes{Value: 50 * gb, Source: model.SourceEstimated}, Explain: "gap"},
			{Name: "Snapshots", Bytes: model.Bytes{Source: model.SourceUnknown}, Explain: "snap"},
		},
	}
	var b strings.Builder
	if err := cli.RenderStorage(&b, res, true); err != nil {
		t.Fatal(err)
	}
	mustContain(t, b.String(), "Nothing to reclaim was found", "3.0 GB (tool)", "~50.0 GB (estimated)", "—  unknown\n", "  None.")
}
