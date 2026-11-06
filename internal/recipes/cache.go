package recipes

import (
	"context"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/platform"
)

// DryRunTTL is how long a cached Homebrew dry run may be reused, provided
// Homebrew's own state (BrewFingerprint) has not changed either.
const DryRunTTL = 10 * time.Minute

// Cached is one stored dry run: the Homebrew fingerprint and time it was
// taken at, and what the command printed.
type Cached struct {
	Fingerprint string
	At          time.Time
	Result      platform.Result
}

// Cache stores dry-run results between commands. It is for DISPLAY
// only. The executor, planner.Verify and the listers must never be given one
// (their Env leaves Env.Cache nil): they have to see what the live tool says
// at the moment they act. A nil cache means no caching. Load reports false
// for a missing or unreadable entry, and Save may fail; neither is ever
// allowed to fail a detector.
type Cache interface {
	Load(key string) (Cached, bool)
	Save(key string, run Cached) error
}

// RunCachedDryRun is RunDryRun for the slow Homebrew dry runs, reusing a
// stored result while it is younger than DryRunTTL and the Homebrew
// fingerprint is unchanged. Without env.Cache, without a recipe dry run or
// when the fingerprint cannot be computed, it simply runs the command. Only
// a successful run (no runner error, exit code 0) is stored.
func RunCachedDryRun(ctx context.Context, env Env, r Recipe, timeout time.Duration) (platform.Result, error) {
	if env.Cache == nil || r.Native == nil || len(r.Native.DryRunArgv) == 0 {
		return RunDryRun(ctx, env, r, timeout)
	}
	bin, err := BrewBinary()
	if err != nil {
		return RunDryRun(ctx, env, r, timeout)
	}
	fp, err := BrewFingerprint(env.Home, bin)
	if err != nil {
		return RunDryRun(ctx, env, r, timeout)
	}
	now := env.Now
	if now.IsZero() {
		now = time.Now()
	}
	key := strings.Join(r.Native.DryRunArgv, " ")
	if c, ok := env.Cache.Load(key); ok && c.Fingerprint == fp {
		if age := now.Sub(c.At); age >= 0 && age < DryRunTTL {
			return c.Result, nil
		}
	}
	res, err := RunDryRun(ctx, env, r, timeout)
	if err == nil && res.ExitCode == 0 {
		_ = env.Cache.Save(key, Cached{Fingerprint: fp, At: now, Result: res}) // best effort
	}
	return res, err
}
