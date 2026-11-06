package tests

import (
	"github.com/sagnik26/macdoc/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadSave(t *testing.T) {
	home := t.TempDir()
	c, err := config.Load(home)
	if err != nil || len(c.Protect) != 0 || c.PlanTTL() != 30*time.Minute || c.NativeTimeout() != 15*time.Minute {
		t.Fatalf("defaults: %+v, %v", c, err)
	}
	if err := c.Set("plan_minutes", 60); err != nil {
		t.Fatal(err)
	}
	if err := c.AddProtect("~/work/keep/"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddProtect("~/work/keep"); err != nil { // same, cleaned
		t.Fatal(err)
	}
	if err := config.Save(home, c); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(config.Path(home))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v, %v", fi, err)
	}
	got, err := config.Load(home)
	if err != nil || got.PlanTTL() != time.Hour || len(got.Protect) != 1 || got.Protect[0] != "~/work/keep" {
		t.Fatalf("reloaded %+v, %v", got, err)
	}
	if err := got.RemoveProtect("~/work/keep"); err != nil || len(got.Protect) != 0 {
		t.Errorf("remove: %v %v", got.Protect, err)
	}
	if err := got.RemoveProtect("~/Documents"); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Errorf("removing a built-in: %v", err)
	}
	if err := got.Set("plan_minutes", 0); err != nil || got.PlanTTL() != 30*time.Minute {
		t.Errorf("reset to default: %v", err)
	}
}

func TestInvalid(t *testing.T) {
	var c config.Config
	for _, bad := range []struct {
		key string
		v   int
	}{{"plan_minutes", 4}, {"plan_minutes", 121}, {"native_timeout_minutes", 61}, {"nope", 1}} {
		if err := c.Set(bad.key, bad.v); err == nil {
			t.Errorf("Set(%s, %d) accepted", bad.key, bad.v)
		}
	}
	for _, p := range []string{"", "relative/dir", "~", "/a/../b", "~/x/../../y"} {
		if err := c.AddProtect(p); err == nil {
			t.Errorf("AddProtect(%q) accepted", p)
		}
	}

	// A broken file fails closed: no defaults, an error naming the file.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(config.Path(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"protect": ["~/a"], "typo": 1}`, `{"plan_minutes": 1000}`, `{"protect": ["rel"]}`, `{`} {
		if err := os.WriteFile(config.Path(home), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.Load(home); err == nil || !strings.Contains(err.Error(), "config.json") {
			t.Errorf("%s: %v", body, err)
		}
		if _, _, err := config.Policy(home); err == nil {
			t.Errorf("%s: policy built from a broken config", body)
		}
	}
}

func TestPolicyProtects(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "work", "keep", "node_modules")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	p, _, err := config.Policy(home)
	if err != nil {
		t.Fatal(err)
	}
	if prot, _ := p.IsProtected(target); prot {
		t.Fatal("protected before configuring")
	}
	c := config.Config{}
	if err := c.AddProtect("~/work/keep"); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(home, c); err != nil {
		t.Fatal(err)
	}
	p, _, err = config.Policy(home)
	if err != nil {
		t.Fatal(err)
	}
	if prot, _ := p.IsProtected(target); !prot {
		t.Error("a folder inside a configured protected folder isn't protected")
	}
	if err := p.CheckTarget(target, []string{"~/**/node_modules"}); err == nil {
		t.Error("CheckTarget allowed a configured protected folder")
	}
}
