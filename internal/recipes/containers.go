package recipes

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/sagnik26/macdoc/internal/model"
)

func init() {
	Register("container_caches", DetectContainerCaches)
}

// DetectContainerCaches emits the Caches folder inside each sandboxed app's
// container: <dir>/<bundle id>/Data/Library/Caches, for the kept ones of at
// least params["min_bytes"]. Apple's own containers (com.apple.*) and the
// comma-separated names in params["exclude"] are skipped. Only that one folder
// is ever listed, never the rest of the container, which holds the app's data.
func DetectContainerCaches(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := RequireParam(r, "dir")
	if err != nil || env.Nodes == nil {
		return nil, err
	}
	minBytes, err := paramInt(r, "min_bytes", 1)
	if err != nil {
		return nil, err
	}
	exclude := paramSet(r, "exclude")
	dir = ExpandHome(env.Home, dir)
	nodes, err := env.Nodes.Nodes(dir, 0)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range nodes {
		id, ok := containerCacheOwner(dir, n.Path)
		if !ok || n.Kind == model.NodeFile || n.Bytes < minBytes ||
			strings.HasPrefix(strings.ToLower(id), "com.apple.") || exclude[strings.ToLower(id)] {
			continue
		}
		f := nodeFinding(n)
		f.Title = id + " cache"
		out = append(out, f)
	}
	return out, nil
}

// containerCacheOwner returns the bundle ID of the container when path is
// exactly <dir>/<id>/Data/Library/Caches (names match without regard to case,
// as on a default macOS volume), and false for anything else.
func containerCacheOwner(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 4 || parts[0] == "" || parts[0] == ".." {
		return "", false
	}
	for i, want := range []string{"Data", "Library", "Caches"} {
		if !strings.EqualFold(parts[i+1], want) {
			return "", false
		}
	}
	return parts[0], true
}
