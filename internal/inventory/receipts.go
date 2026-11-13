package inventory

import (
	"context"
	"strconv"
	"strings"
	"time"
)

func init() { Register(receiptsSource{}) }

// receiptsSource lists the packages macOS recorded when an installer ran
// (`pkgutil`). They cover software that isn't a bundle in /Applications, such
// as command-line tools and frameworks installed from a .pkg.
type receiptsSource struct{}

// Name implements Source.
func (receiptsSource) Name() string { return "pkgutil" }

// Discover implements Source. A package whose details can't be read is still
// listed, without a version.
func (receiptsSource) Discover(ctx context.Context, env Env) ([]Software, error) {
	if !hasTool(env, "pkgutil") {
		return nil, errAbsent{"pkgutil"}
	}
	out, err := output(ctx, env, "pkgutil", "--pkgs")
	if err != nil {
		return nil, err
	}
	var res []Software
	for _, id := range lines(out) {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		sw := Software{ID: "package:" + id, Name: id, Kind: "package", Source: "pkgutil", Evidence: "pkgutil --pkg-info " + id}
		if info, err := output(ctx, env, "pkgutil", "--pkg-info", id); err == nil {
			sw.Version, sw.Installed = receiptFields(string(info))
		}
		res = append(res, sw)
	}
	return res, nil
}

// receiptFields reads the version and install time from `pkgutil --pkg-info`.
func receiptFields(info string) (version string, installed time.Time) {
	for _, l := range strings.Split(info, "\n") {
		key, val, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "version":
			version = val
		case "install-time":
			if sec, err := strconv.ParseInt(val, 10, 64); err == nil {
				installed = time.Unix(sec, 0)
			}
		}
	}
	return version, installed
}
