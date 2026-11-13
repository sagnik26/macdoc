package inventory

import (
	"context"
	"strings"
)

func init() {
	Register(dockerSource{})
	Register(ollamaSource{})
}

// dockerSource lists the Docker images on this Mac. It asks the daemon, so
// when Docker isn't running the source is reported as unknown, not as empty.
type dockerSource struct{}

// Name implements Source.
func (dockerSource) Name() string { return "docker" }

// Discover implements Source.
func (dockerSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if !hasTool(env, "docker") {
		return nil, errAbsent{"docker"}
	}
	out, err := output(ctx, env, "docker", "images", "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		return nil, err
	}
	var res []Software
	for _, l := range lines(out) {
		if strings.HasPrefix(l, "<none>") {
			continue
		}
		res = append(res, Software{ID: "image:" + l, Name: l, Kind: "image", Source: "docker",
			Evidence: "docker images"})
	}
	return res, nil
}

// ollamaSource lists the local language models Ollama has pulled. The first
// line is a header; each model is a row whose first column is its name.
type ollamaSource struct{}

// Name implements Source.
func (ollamaSource) Name() string { return "ollama" }

// Discover implements Source.
func (ollamaSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if !hasTool(env, "ollama") {
		return nil, errAbsent{"ollama"}
	}
	out, err := output(ctx, env, "ollama", "list")
	if err != nil {
		return nil, err
	}
	var res []Software
	for i, l := range lines(out) {
		f := strings.Fields(l)
		if i == 0 || len(f) == 0 {
			continue
		}
		res = append(res, Software{ID: "model:" + f[0], Name: f[0], Kind: "model", Source: "ollama",
			Evidence: "ollama list"})
	}
	return res, nil
}
