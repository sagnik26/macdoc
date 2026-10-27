package recipes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"macdoc/internal/model"
	"macdoc/internal/platform"
)

func init() {
	Register("docker_df", detectDockerDF)
	Register("docker_dangling", detectDockerDangling)
}

// dockerJSON is Docker's per-row Go template; argv has no shell, so no quotes.
const dockerJSON = "{{json .}}"

// dockerDFRow is one line of `docker system df --format {{json .}}`, e.g.
// {"Active":"2","Reclaimable":"1.1GB (45%)","Size":"2.3GB","TotalCount":"5","Type":"Images"}
// (testdata/w2_docker_system_df.txt).
type dockerDFRow struct {
	Type        string
	TotalCount  json.Number
	Active      json.Number
	Size        string
	Reclaimable string
}

// dockerImageRow is one line of `docker images -f dangling=true --format
// {{json .}}` (testdata/w2_docker_dangling.txt). Only the fields used here.
type dockerImageRow struct {
	ID   string
	Size string
}

// runDocker runs a read-only docker listing. ok is false when docker isn't
// installed or the daemon isn't reachable.
func runDocker(ctx context.Context, env Env, args ...string) (out []byte, ok bool, err error) {
	if env.Runner == nil || !ToolInstalled("docker") {
		return nil, false, nil
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: "docker", Args: args})
	if err != nil {
		return nil, false, err
	}
	if res.ExitCode != 0 {
		msg := strings.ToLower(string(res.Stderr) + string(res.Stdout))
		if strings.Contains(msg, "connect") && (strings.Contains(msg, "daemon") || strings.Contains(msg, "docker api")) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("docker %s: exit %d: %s", args[0], res.ExitCode, firstLine(res.Stderr))
	}
	return res.Stdout, true, nil
}

// jsonLines decodes one JSON object per non-empty line.
func jsonLines[T any](b []byte, what string) ([]T, error) {
	var out []T
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// detectDockerDF emits the row of `docker system df` named by
// params["type"]. Native recipes report the reclaimable size; the advise
// recipe for volumes reports the total size, since nothing is reclaimed.
// Docker's sizes are decimal (go-units).
func detectDockerDF(ctx context.Context, env Env, r Recipe) ([]Finding, error) {
	typ, err := requireParam(r, "type")
	if err != nil {
		return nil, err
	}
	out, ok, err := runDocker(ctx, env, "system", "df", "--format", dockerJSON)
	if !ok {
		return nil, err
	}
	rows, err := jsonLines[dockerDFRow](out, "docker system df")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Type != typ {
			continue
		}
		size := row.Reclaimable
		if r.Method == model.MethodAdvise {
			size = row.Size
		}
		b, ok := parseSize(size, 1000)
		if !ok {
			return nil, fmt.Errorf("docker system df: %s: bad size %q", typ, size)
		}
		if b == 0 {
			return nil, nil
		}
		return []Finding{{
			Bytes: model.Bytes{Value: b, Source: model.SourceTool},
			Note:  fmt.Sprintf("%s total, %s in use (Docker reports %s total).", row.TotalCount, row.Active, row.Size),
		}}, nil
	}
	return nil, nil
}

// detectDockerDangling sums the sizes of untagged images. Shared layers may
// be counted more than once, so the total is an estimate.
func detectDockerDangling(ctx context.Context, env Env, _ Recipe) ([]Finding, error) {
	out, ok, err := runDocker(ctx, env, "images", "-f", "dangling=true", "--format", dockerJSON)
	if !ok {
		return nil, err
	}
	rows, err := jsonLines[dockerImageRow](out, "docker images")
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	f := Finding{Note: plural(len(rows), "untagged image", "untagged images")}
	var total int64
	for _, row := range rows {
		b, ok := parseSize(row.Size, 1000)
		if !ok {
			f.Bytes = model.Bytes{Source: model.SourceUnknown, Reason: fmt.Sprintf("docker reported an unreadable size %q", row.Size)}
			return []Finding{f}, nil
		}
		total += b
	}
	f.Bytes = model.Bytes{Value: total, Source: model.SourceEstimated, Reason: "sum of image sizes; shared layers may be counted more than once"}
	return []Finding{f}, nil
}
