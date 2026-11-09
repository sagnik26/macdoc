package engine

import (
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
)

// dryRuns implements recipes.DryRunCache over the state database. It is
// handed only to display detectors (Opportunities); apply and plan
// re-validation build their own Env without it.
type dryRuns struct{ st *store.Store }

// Load returns the stored run for key; any read problem is a miss.
func (d dryRuns) Load(key string) (recipes.Cached, bool) {
	r, ok, err := d.st.DryRun(key)
	if err != nil || !ok {
		return recipes.Cached{}, false
	}
	return recipes.Cached{Fingerprint: r.Fingerprint, At: r.At,
		Result: platform.Result{ExitCode: r.ExitCode, Stdout: r.Stdout, Stderr: r.Stderr}}, true
}

// Save stores run under key.
func (d dryRuns) Save(key string, run recipes.Cached) error {
	return d.st.SaveDryRun(store.DryRun{Key: key, Fingerprint: run.Fingerprint, At: run.At,
		ExitCode: run.Result.ExitCode, Stdout: run.Result.Stdout, Stderr: run.Result.Stderr})
}
