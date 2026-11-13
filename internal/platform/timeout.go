package platform

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Bounds on a timeout override from the environment.
const (
	MinTimeout = 1 * time.Second
	MaxTimeout = 10 * time.Minute
)

// TimeoutEnv is the global override, in whole seconds. MACDOC_TIMEOUT_<NAME>
// (the command's name upper-cased, other characters as "_", such as
// MACDOC_TIMEOUT_SYSTEM_PROFILER) overrides it for one command.
const TimeoutEnv = "MACDOC_TIMEOUT"

// TimeoutFor is the timeout for the read-only command name. The per-command
// variable wins over the global one, which wins over def (DefaultTimeout when
// def is not positive). A value that is not a whole number of seconds within
// MinTimeout..MaxTimeout is ignored. lookup reads the environment.
func TimeoutFor(name string, def time.Duration, lookup func(string) (string, bool)) time.Duration {
	if def <= 0 {
		def = DefaultTimeout
	}
	if lookup == nil {
		return def
	}
	for _, key := range []string{TimeoutEnv + "_" + envName(name), TimeoutEnv} {
		if v, ok := lookup(key); ok {
			if d, ok := parseTimeout(v); ok {
				return d
			}
		}
	}
	return def
}

func parseTimeout(v string) (time.Duration, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	d := time.Duration(n) * time.Second
	if n <= 0 || d < MinTimeout || d > MaxTimeout {
		return 0, false
	}
	return d, true
}

// envName turns a command name or path into the NAME part of a variable.
func envName(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// envLookup reads the process environment.
func envLookup(k string) (string, bool) { return os.LookupEnv(k) }
