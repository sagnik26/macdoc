package tests

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// reclaimFixture is a sandboxed home with one folder at each risk level, a
// scan from two hours ago, and a catalog that finds exactly those folders.
func reclaimFixture(t *testing.T) (home string, safe, caution, risky string) {
	t.Helper()
	home = cliFixture(t)
	sandboxFor(t, home)
	cli.Runner = &platform.Fake{Responses: map[string]platform.Result{"ps -axww -o command=": {Stdout: []byte("/sbin/launchd\n")}}}
	old := time.Now().Add(-200 * 24 * time.Hour)
	mk := func(name string) string {
		p := filepath.Join(home, "code", name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "f"), make([]byte, 4096), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		return p
	}
	safe, caution, risky = mk("safe-cache"), mk("caution-cache"), mk("risky-cache")
	cat := &recipes.Catalog{Recipes: []recipes.Recipe{
		cliRecipe("t.safe", "Safe cache", model.RiskSafe, model.MethodQuarantine, model.RestoreRedownload),
		cliRecipe("t.caution", "Caution cache", model.RiskCaution, model.MethodQuarantine, model.RestoreRedownload),
		cliRecipe("t.risky", "Risky cache", model.RiskRisky, model.MethodQuarantine, model.RestoreRedownload),
	}}
	cliMu.Lock()
	cliFindings = map[string][]recipes.Finding{
		"t.safe":    {{Target: safe, Bytes: measured(gb), LastUsed: old}},
		"t.caution": {{Target: caution, Bytes: measured(gb), LastUsed: old}},
		"t.risky":   {{Target: risky, Bytes: measured(gb), LastUsed: old}},
	}
	cliMu.Unlock()
	cli.LoadCatalog = func() (*recipes.Catalog, error) { return cat, nil }
	return home, safe, caution, risky
}

// Enter cleans the SAFE items it showed, records them in the audit log, and
// leaves the CAUTION and RISKY ones where they are.
func TestReclaimEnterCleansSafeItems(t *testing.T) {
	home, safe, caution, risky := reclaimFixture(t)
	termOn(t, true)
	out, err := runWithInput(t, "\n", "reclaim")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Safe cache", "What do you want to clean?", "SAFE", "CAUTION", "RISKY", "ADVICE", "can't remove", "Cleaning SAFE: 1 item", "applied: 1 done")
	if present(safe) {
		t.Error("the SAFE folder is still there after Enter")
	}
	if !present(caution) || !present(risky) {
		t.Error("a CAUTION or RISKY folder was cleaned")
	}
	log, err := os.ReadFile(filepath.Join(home, "Library", "Application Support", "macdoc", "audit.jsonl"))
	if err != nil || !strings.Contains(string(log), "t.safe") {
		t.Errorf("the audit log doesn't record the cleaning: %v\n%s", err, log)
	}
}

// Anything but a bare Enter cancels, seals nothing and removes nothing.
func TestReclaimOtherKeyCancels(t *testing.T) {
	home, safe, caution, risky := reclaimFixture(t)
	termOn(t, true)
	for _, answer := range []string{"n\n", "q\n", "no thanks\n"} {
		out, err := runWithInput(t, answer, "reclaim")
		if err != nil || !strings.Contains(out, "Canceled. Nothing has been changed.") {
			t.Fatalf("answer %q: %v\n%s", answer, err, out)
		}
	}
	if _, err := runWithInput(t, "", "reclaim"); err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Errorf("no answer at all: err = %v", err)
	}
	if !present(safe) || !present(caution) || !present(risky) {
		t.Error("something was removed without an Enter")
	}
	if recs, _ := engine.ListPlans(engine.OpportunityOptions{Home: home}, 5); len(recs) != 0 {
		t.Errorf("a plan was sealed although nothing was confirmed: %+v", recs)
	}
}

// Without a real terminal, and with --json, --quiet or --dry-run, reclaim only
// lists: a script, a pipe or an agent can never clean through it, even when
// an Enter is waiting on stdin.
func TestReclaimNeverCleansWithoutATerminal(t *testing.T) {
	_, safe, caution, risky := reclaimFixture(t)
	termOn(t, false)
	out, err := runWithInput(t, "\n", "reclaim")
	if err != nil || strings.Contains(out, "Press Enter") {
		t.Fatalf("piped reclaim must only list: %v\n%s", err, out)
	}
	mustContain(t, out, "Safe cache")
	termOn(t, true)
	for _, args := range [][]string{{"reclaim", "--json"}, {"reclaim", "--quiet"}, {"reclaim", "--dry-run"}} {
		out, err := runWithInput(t, "\n", args...)
		if err != nil || strings.Contains(out, "Press Enter") {
			t.Fatalf("%v must only list: %v\n%s", args, err, out)
		}
	}
	if !present(safe) || !present(caution) || !present(risky) {
		t.Error("something was removed without a terminal or after a view-only flag")
	}
}

// Enter never cleans a higher tier than the one shown: the default is SAFE,
// --tier caution cleans only CAUTION, and RISKY items are never cleaned by
// reclaim at all.
func TestReclaimNeverCleansCautionOrRisky(t *testing.T) {
	_, safe, caution, risky := reclaimFixture(t)
	termOn(t, true)
	if out, err := runWithInput(t, "\n", "reclaim", "--tier", "risky"); err != nil || strings.Contains(out, "Press Enter") {
		t.Fatalf("--tier risky must only list: %v\n%s", err, out)
	}
	if !present(risky) {
		t.Fatal("reclaim --tier risky removed a RISKY folder")
	}
	out, err := runWithInput(t, "\n", "reclaim", "--tier", "caution")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Caution cache", "applied: 1 done")
	if present(caution) {
		t.Error("--tier caution did not clean the CAUTION folder")
	}
	if !present(safe) || !present(risky) {
		t.Error("--tier caution cleaned a SAFE or RISKY folder it did not show")
	}
}

// With no scan to work from, reclaim at a terminal scans first instead of
// failing, and still asks before cleaning anything.
func TestReclaimScansFirstWhenThereIsNoScan(t *testing.T) {
	home := cliFixture(t)
	sandboxFor(t, home)
	empty := filepath.Join(home, "empty")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	cli.UserHome = func() (string, error) { return empty, nil }
	scanRoot := t.TempDir()
	old := cli.ScanRoot
	cli.ScanRoot = scanRoot
	t.Cleanup(func() { cli.ScanRoot = old })
	termOn(t, true)
	out, err := runWithInput(t, "n\n", "reclaim")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Scanning your Mac first")
}

// interactive runs reclaim with a person's answers: the first line is typed at
// once, and each later answer is typed when its prompt appears. A reply of
// "<code>" is replaced by the plan's code taken from the prompt.
func interactive(t *testing.T, first string, then []struct{ prompt, reply string }, args ...string) (string, error) {
	t.Helper()
	pr, pw := io.Pipe()
	var out syncBuf
	cmd := cli.NewRootCmd()
	cmd.SetIn(pr)
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	_, _ = pw.Write([]byte(first))
	for _, step := range then {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) && !strings.Contains(out.String(), step.prompt) {
			select {
			case err := <-done:
				return out.String(), err
			case <-time.After(20 * time.Millisecond):
			}
		}
		reply := step.reply
		if reply == "<code>" {
			s := out.String()
			i := strings.LastIndex(s, `Type "`)
			code, _, _ := strings.Cut(s[i+len(`Type "`):], `"`)
			reply = code + "\n"
		}
		_, _ = pw.Write([]byte(reply))
	}
	err := <-done
	return out.String(), err
}

// Several risk types can be chosen at once; what is not chosen stays.
func TestReclaimChoosesSeveralRiskTypes(t *testing.T) {
	_, safe, caution, risky := reclaimFixture(t)
	termOn(t, true)
	out, err := runWithInput(t, "safe, caution\n", "reclaim")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Cleaning SAFE and CAUTION: 2 items", "applied: 2 done")
	if present(safe) || present(caution) {
		t.Error("a chosen risk type was not cleaned")
	}
	if !present(risky) {
		t.Error("RISKY was cleaned although it was not chosen")
	}
}

// Choosing RISKY does not skip its confirmations: each RISKY item asks y/N,
// the plan's code is typed, and a "no" leaves the item where it is.
func TestReclaimRiskyKeepsItsConfirmations(t *testing.T) {
	_, safe, caution, risky := reclaimFixture(t)
	termOn(t, true)
	out, err := interactive(t, "risky\n", []struct{ prompt, reply string }{{"[y/N]", "n\n"}}, "reclaim")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Cleaning RISKY: 1 item", "[y/N]", "Nothing approved")
	if !present(risky) || !present(safe) || !present(caution) {
		t.Error("a RISKY item was cleaned without its own yes")
	}

	out, err = interactive(t, "all\n", []struct{ prompt, reply string }{{"[y/N]", "y\n"}, {`Type "`, "<code>"}}, "reclaim")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Cleaning SAFE, CAUTION and RISKY: 3 items", "applied: 3 done")
	if present(safe) || present(caution) || present(risky) {
		t.Error("with each confirmation given, all three should be cleaned")
	}
}

// Advice-only items can't be chosen, and "all" never includes them; an answer
// reclaim doesn't understand cancels without cleaning anything.
func TestReclaimAdviceCannotBeCleaned(t *testing.T) {
	_, safe, caution, risky := reclaimFixture(t)
	termOn(t, true)
	out, err := runWithInput(t, "advice\n", "reclaim")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, "Advice-only items can't be removed", "Canceled. Nothing has been changed.")
	out, err = runWithInput(t, "banana\n", "reclaim")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustContain(t, out, `I didn't understand "banana"`, "Canceled. Nothing has been changed.")
	if !present(safe) || !present(caution) || !present(risky) {
		t.Error("something was cleaned on an answer that chose nothing")
	}
}
