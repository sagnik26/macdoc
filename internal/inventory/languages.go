package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	Register(pipSource{})
	Register(pipxSource{})
	Register(uvToolSource{})
	Register(gemSource{})
	Register(crateSource{})
}

// The language package managers below are only asked to list what is
// installed, with fixed read-only commands.

// pipSource lists the Python packages that pip3 installed.
type pipSource struct{}

// Name implements Source.
func (pipSource) Name() string { return "pip" }

// Discover implements Source.
func (pipSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if !hasTool(env, "pip3") {
		return nil, errAbsent{"pip3"}
	}
	out, err := output(ctx, env, "pip3", "list", "--format=json")
	if err != nil {
		return nil, err
	}
	var pkgs []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &pkgs); err != nil {
		return nil, fmt.Errorf("pip3 list: %w", err)
	}
	var res []Software
	for _, p := range pkgs {
		res = append(res, Software{ID: "pip:" + p.Name, Name: p.Name, Kind: "pip", Version: p.Version,
			Source: "pip", Evidence: "pip3 list"})
	}
	return res, nil
}

// pipxSource lists the applications installed with pipx, one per virtualenv.
type pipxSource struct{}

// Name implements Source.
func (pipxSource) Name() string { return "pipx" }

// Discover implements Source.
func (pipxSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if !hasTool(env, "pipx") {
		return nil, errAbsent{"pipx"}
	}
	out, err := output(ctx, env, "pipx", "list", "--json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Venvs map[string]struct {
			Metadata struct {
				MainPackage struct {
					Package        string `json:"package"`
					PackageVersion string `json:"package_version"`
				} `json:"main_package"`
			} `json:"metadata"`
		} `json:"venvs"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("pipx list: %w", err)
	}
	var res []Software
	for name, v := range doc.Venvs {
		mp := v.Metadata.MainPackage
		if mp.Package == "" {
			mp.Package = name
		}
		res = append(res, Software{ID: "pipx:" + mp.Package, Name: mp.Package, Kind: "pipx",
			Version: mp.PackageVersion, Source: "pipx", Evidence: "pipx list"})
	}
	return res, nil
}

// uvToolSource lists the tools installed with `uv tool`. Each tool is a line
// "name vX.Y"; the indented lines under it are its commands and are skipped.
type uvToolSource struct{}

// Name implements Source.
func (uvToolSource) Name() string { return "uv-tool" }

// Discover implements Source.
func (uvToolSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if !hasTool(env, "uv") {
		return nil, errAbsent{"uv"}
	}
	out, err := output(ctx, env, "uv", "tool", "list")
	if err != nil {
		return nil, err
	}
	var res []Software
	for _, l := range lines(out) {
		if strings.HasPrefix(l, "-") || strings.HasPrefix(l, " ") {
			continue
		}
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		ver := ""
		if len(f) > 1 {
			ver = strings.TrimPrefix(f[1], "v")
		}
		res = append(res, Software{ID: "uv-tool:" + f[0], Name: f[0], Kind: "uv-tool", Version: ver,
			Source: "uv-tool", Evidence: "uv tool list"})
	}
	return res, nil
}

// gemSource lists the RubyGems installed on this Mac. Each line is
// "name (version, other versions)".
type gemSource struct{}

// Name implements Source.
func (gemSource) Name() string { return "gem" }

// Discover implements Source.
func (gemSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if !hasTool(env, "gem") {
		return nil, errAbsent{"gem"}
	}
	out, err := output(ctx, env, "gem", "list", "--local", "--no-details")
	if err != nil {
		return nil, err
	}
	var res []Software
	for _, l := range lines(out) {
		name, rest, ok := strings.Cut(l, " (")
		if !ok {
			continue
		}
		ver, _, _ := strings.Cut(strings.TrimSuffix(rest, ")"), ",")
		res = append(res, Software{ID: "gem:" + name, Name: name, Kind: "gem", Version: strings.TrimSpace(ver),
			Source: "gem", Evidence: "gem list"})
	}
	return res, nil
}

// crateSource lists the Rust programs installed with `cargo install`. A crate
// is a line "name vX.Y.Z:"; its binaries are indented lines under it.
type crateSource struct{}

// Name implements Source.
func (crateSource) Name() string { return "cargo" }

// Discover implements Source.
func (crateSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if !hasTool(env, "cargo") {
		return nil, errAbsent{"cargo"}
	}
	out, err := output(ctx, env, "cargo", "install", "--list")
	if err != nil {
		return nil, err
	}
	var res []Software
	for _, l := range lines(out) {
		if strings.HasPrefix(l, " ") || !strings.HasSuffix(l, ":") {
			continue
		}
		f := strings.Fields(strings.TrimSuffix(l, ":"))
		if len(f) == 0 {
			continue
		}
		ver := ""
		if len(f) > 1 {
			ver = strings.TrimPrefix(f[1], "v")
		}
		res = append(res, Software{ID: "crate:" + f[0], Name: f[0], Kind: "crate", Version: ver,
			Source: "cargo", Evidence: "cargo install --list"})
	}
	return res, nil
}
