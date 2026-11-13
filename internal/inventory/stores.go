package inventory

import (
	"context"
	"regexp"
)

func init() { Register(storeSource{}) }

// storeSource lists the App Store apps `mas` knows about. A line is
// "<id> <name> (<version>)".
type storeSource struct{}

var storeLine = regexp.MustCompile(`^(\d+)\s+(.+?)\s+\(([^)]*)\)\s*$`)

// Name implements Source.
func (storeSource) Name() string { return "mas" }

// Discover implements Source.
func (storeSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if !hasTool(env, "mas") {
		return nil, errAbsent{"mas"}
	}
	out, err := output(ctx, env, "mas", "list")
	if err != nil {
		return nil, err
	}
	var res []Software
	for _, l := range lines(out) {
		m := storeLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		res = append(res, Software{ID: "store:" + m[1], Name: m[2], Kind: "app", Version: m[3],
			Source: "mas", Evidence: "mas list"})
	}
	return res, nil
}
