package inventory

import (
	"context"
	"strconv"
	"strings"

	"github.com/sagnik26/macdoc/internal/platform"
)

// slimOf reads which architectures an executable holds with lipo, which is
// read-only. It returns nil when lipo is missing or fails, so the answer stays
// unknown. When the file holds both arm64 and x86_64, the size of the Intel
// slice comes from lipo -detailed_info; if that fails the size stays unknown.
func slimOf(ctx context.Context, env Env, exe string) *Slim {
	if env.Runner == nil {
		return nil
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: "lipo", Args: []string{"-archs", exe}})
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	archs := strings.Fields(string(res.Stdout))
	if len(archs) == 0 {
		return nil
	}
	s := &Slim{Archs: archs}
	var intel, arm bool
	for _, a := range archs {
		switch {
		case a == "x86_64":
			intel = true
		case strings.HasPrefix(a, "arm64"):
			arm = true
		}
	}
	s.Universal = intel && arm
	if !s.Universal {
		return s
	}
	det, err := env.Runner.Run(ctx, platform.Command{Name: "lipo", Args: []string{"-detailed_info", exe}})
	if err != nil || det.ExitCode != 0 {
		return s
	}
	if n, ok := sliceSize(string(det.Stdout), "x86_64"); ok {
		s.IntelBytes = &n
	}
	return s
}

// sliceSize finds the "size" line of one architecture in lipo -detailed_info
// output. ok is false when the architecture or its size isn't there.
func sliceSize(out, arch string) (int64, bool) {
	in := false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "architecture" {
			in = f[1] == arch
			continue
		}
		if in && len(f) == 2 && f[0] == "size" {
			n, err := strconv.ParseInt(f[1], 10, 64)
			return n, err == nil && n >= 0
		}
	}
	return 0, false
}
