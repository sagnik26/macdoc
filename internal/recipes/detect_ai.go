package recipes

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/platform"
)

func init() {
	Register("ollama_models", detectOllamaModels)
	Register("lmstudio_models", DetectLMStudioModels)
}

// OllamaNameRe accepts library model names as `ollama list` prints them
// ("llama3.2:latest"). Names with "/" (namespaced or hf.co models),
// whitespace, a leading "-" or shell characters are rejected.
var OllamaNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(:[A-Za-z0-9._-]+)?$`)

// ollamaDateLayouts are the absolute MODIFIED formats accepted; relative
// ones ("3 weeks ago") are parsed by OllamaRelative.
var ollamaDateLayouts = []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}

// detectOllamaModels parses `ollama list` (testdata/w2_ollama_list.txt):
//
//	NAME               ID              SIZE      MODIFIED
//	llama3.2:latest    a80c4f17acd5    2.0 GB    3 weeks ago
//
// NativeArg is the NAME column verbatim; SIZE is decimal (SourceTool).
func detectOllamaModels(ctx context.Context, env Env, _ Recipe) ([]Finding, error) {
	if env.Runner == nil || !ToolInstalled("ollama") {
		return nil, nil
	}
	res, err := env.Runner.Run(ctx, platform.Command{Name: "ollama", Args: []string{"list"}})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(strings.ToLower(string(res.Stderr)), "connect") {
			return nil, nil // the Ollama server isn't running
		}
		return nil, fmt.Errorf("ollama list: exit %d: %s", res.ExitCode, FirstLine(res.Stderr))
	}
	var out []Finding
	for i, line := range strings.Split(string(res.Stdout), "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 4 || !OllamaNameRe.MatchString(f[0]) {
			continue
		}
		name, size, modified := f[0], f[2], ""
		next := 3
		if isSizeUnit(f[3]) { // "2.0 GB" split by Fields
			size += f[3]
			next = 4
		}
		if next < len(f) {
			modified = strings.Join(f[next:], " ")
		}
		fd := Finding{NativeArg: name, Title: "Ollama model " + name}
		if b, ok := ParseSize(size, 1000); ok && b > 0 {
			fd.Bytes = model.Bytes{Value: b, Source: model.SourceTool}
		} else {
			fd.Bytes = model.Bytes{Source: model.SourceUnknown, Reason: fmt.Sprintf("ollama list reported size %q", size)}
		}
		for _, layout := range ollamaDateLayouts {
			if t, err := time.Parse(layout, modified); err == nil {
				fd.LastUsed = t.UTC()
				break
			}
		}
		if d, ok := OllamaRelative(modified); fd.LastUsed.IsZero() && ok {
			fd.LastUsed = env.Clock().Add(-d).UTC()
		}
		if fd.LastUsed.IsZero() && modified != "" {
			fd.Note = "Last modified " + modified + "."
		}
		out = append(out, fd)
	}
	return out, nil
}

// ollamaRelativeRe matches "3 weeks ago" and "1 day ago".
var ollamaRelativeRe = regexp.MustCompile(`^(\d{1,4}) (second|minute|hour|day|week|month|year)s? ago$`)

// ollamaUnits are approximate: a month is 30 days and a year 365.
var ollamaUnits = map[string]time.Duration{
	"second": time.Second, "minute": time.Minute, "hour": time.Hour,
	"day": 24 * time.Hour, "week": 7 * 24 * time.Hour,
	"month": 30 * 24 * time.Hour, "year": 365 * 24 * time.Hour,
}

// OllamaRelative parses ollama's relative MODIFIED forms into an
// approximate age. Anything else is unknown (ok false).
func OllamaRelative(s string) (time.Duration, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "less than a second ago":
		return 0, true
	case "about a minute ago":
		return time.Minute, true
	case "about an hour ago":
		return time.Hour, true
	case "yesterday":
		return 24 * time.Hour, true
	}
	m := ollamaRelativeRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return time.Duration(n) * ollamaUnits[m[2]], true
}

// isSizeUnit reports whether s is a bare unit such as "GB" or "kB".
func isSizeUnit(s string) bool {
	return strings.HasSuffix(strings.ToUpper(s), "B") && strings.Trim(strings.ToUpper(s), "KMGTIB") == ""
}

// DetectLMStudioModels emits model directories <root>/<publisher>/<model>
// for each comma-separated root in params["dirs"]. The recipe keeps the
// publisher directories (keep_children_of the roots); model directories are
// seen when the scan keeps them, which it does for anything at or above its
// keep threshold (100 MB by default), so a two-level walk of env.Nodes finds
// them. Publisher names aren't known statically, so keep_children_of can't
// name them.
func DetectLMStudioModels(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dirs, err := RequireParam(r, "dirs")
	if err != nil || env.Nodes == nil {
		return nil, err
	}
	var out []Finding
	for _, root := range strings.Split(dirs, ",") {
		root = ExpandHome(env.Home, strings.TrimSpace(root))
		nodes, err := env.Nodes.Nodes(root, 2)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			if n.Kind == model.NodeFile || filepath.Dir(filepath.Dir(n.Path)) != root || n.Bytes <= 0 {
				continue
			}
			f := nodeFinding(n)
			f.Title = "LM Studio model " + filepath.Base(filepath.Dir(n.Path)) + "/" + filepath.Base(n.Path)
			out = append(out, f)
		}
	}
	return out, nil
}
