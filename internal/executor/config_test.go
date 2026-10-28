package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"macdoc/internal/config"
	"macdoc/internal/model"
)

// A folder protected after the plan was made is never touched, and a broken
// settings file stops apply before anything changes.
func TestConfigProtectAtApply(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	var c config.Config
	if err := c.AddProtect("~/Library/Logs/A"); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(e.home, c); err != nil {
		t.Fatal(err)
	}
	rep, err := Apply(context.Background(), e.cfg, p, all(p))
	if err == nil && rep.Count(OutcomeDone) != 0 {
		t.Fatalf("protected item removed: %+v", rep)
	}
	if !exists(target) {
		t.Fatal("protected folder was moved")
	}

	e = newEnv(t)
	target = e.folder(t, "Library/Logs/A")
	p = e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if err := os.MkdirAll(filepath.Dir(config.Path(e.home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(e.home), []byte(`{"protect": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), e.cfg, p, all(p)); err == nil {
		t.Fatal("apply ran with a broken settings file")
	}
	if !exists(target) || len(e.events(t)) != 0 {
		t.Error("a broken settings file must stop apply before any change or audit record")
	}
}
