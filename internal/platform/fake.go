package platform

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// FakeRunner returns canned results keyed by "name arg1 arg2 ...". It records
// every call so tests can assert exactly what would have been run.
type Fake struct {
	mu        sync.Mutex
	Responses map[string]Result
	Errors    map[string]error
	Calls     []Command
}

// Key returns the lookup key for a command.
func Key(cmd Command) string {
	return strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
}

// Run implements Runner.
func (f *Fake) Run(_ context.Context, cmd Command) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, cmd)
	k := Key(cmd)
	if err, ok := f.Errors[k]; ok {
		return Result{}, err
	}
	if res, ok := f.Responses[k]; ok {
		return res, nil
	}
	return Result{}, fmt.Errorf("fake runner: no response for %q", k)
}
