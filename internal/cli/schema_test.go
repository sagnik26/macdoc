package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"macdoc/internal/engine"
	"macdoc/internal/executor"
	"macdoc/internal/model"
	"macdoc/internal/store"
)

// The JSON contract (CLI_UX §5): every --json output is an envelope whose
// data has a fixed shape, frozen as schema/<command>.json at schema_version
// 1. Changing a data type changes its generated schema and fails
// TestSchemasFrozen; regenerate with
//
//	go test ./internal/cli -run TestSchemasFrozen -update
//
// only for a deliberate, additive change (new optional fields). Renaming or
// removing a field, or changing its type, needs a schema_version bump.
var updateSchemas = flag.Bool("update", false, "rewrite schema/*.json from the Go types")

// jsonCommands maps each envelope command name to its data type.
var jsonCommands = map[string]any{
	"version":            map[string]string{},
	"doctor":             engine.OverviewResult{},
	"doctor scan":        FullResult{},
	"doctor health":      engine.DoctorResult{},
	"doctor slow":        engine.SlowResult{},
	"doctor trend":       engine.TrendResult{},
	"storage":            engine.StorageResult{},
	"wins":               engine.OpportunityResult{},
	"dev":                DevResult{},
	"projects":           engine.ProjectsResult{},
	"explain":            ExplainResult{},
	"plan":               engine.PlanResult{},
	"plan show":          engine.PlanResult{},
	"plan list":          []store.PlanRecord{},
	"log":                engine.AuditResult{},
	"quarantine list":    engine.QuarantineResult{},
	"quarantine restore": executor.EntryResult{},
	"config":             configView{},
}

func schemaFile(command string) string {
	return filepath.Join("..", "..", "schema", strings.ReplaceAll(command, " ", "-")+".json")
}

func TestSchemasFrozen(t *testing.T) {
	for cmd, v := range jsonCommands {
		got, err := json.MarshalIndent(envelopeSchema(cmd, reflect.TypeOf(v)), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := schemaFile(cmd)
		if *updateSchemas {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v (run with -update to create it)", cmd, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: the JSON output of `%s` changed. If that's deliberate and additive, run go test ./internal/cli -run TestSchemasFrozen -update; otherwise bump schema_version", path, cmd)
		}
	}
	// No stray schema files.
	files, _ := filepath.Glob(filepath.Join("..", "..", "schema", "*.json"))
	for _, f := range files {
		if !slices.ContainsFunc(slices.Collect(maps.Keys(jsonCommands)), func(c string) bool { return schemaFile(c) == f }) {
			t.Errorf("%s has no command", f)
		}
	}
}

// Every writeJSON call names a command that has a schema.
func TestEveryJSONCommandHasASchema(t *testing.T) {
	re := regexp.MustCompile(`writeJSON(?:Warn)?\([^,]+, "([^"]+)"`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			seen++
			if _, ok := jsonCommands[m[1]]; !ok {
				t.Errorf("%s: command %q has no schema in jsonCommands", f, m[1])
			}
		}
	}
	if seen < len(jsonCommands) {
		t.Errorf("found %d writeJSON calls for %d schemas: the pattern is stale", seen, len(jsonCommands))
	}
}

// Real output validates against the frozen schemas.
func TestJSONOutputMatchesSchema(t *testing.T) {
	cliFixture(t)
	for _, args := range [][]string{
		{"version"}, {"doctor"}, {"health"}, {"slow"}, {"wins", "--all"}, {"dev"}, {"storage"},
		{"doctor", "projects"}, {"config"}, {"log"}, {"plan", "list"}, {"quarantine", "list"},
	} {
		out, err := run(t, append(args, "--json")...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		cmd, _ := env["command"].(string)
		b, err := os.ReadFile(schemaFile(cmd))
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatal(err)
		}
		if errs := validate(schema, schema, env, "$"); len(errs) > 0 {
			t.Errorf("%v: output doesn't match %s:\n  %s", args, schemaFile(cmd), strings.Join(errs, "\n  "))
		}
	}
}

// --- a small JSON Schema generator for the Go types ---

var timeType = reflect.TypeOf(time.Time{})

func envelopeSchema(command string, data reflect.Type) map[string]any {
	defs := map[string]any{}
	return map[string]any{
		"$schema":  "https://json-schema.org/draft/2020-12/schema",
		"$id":      "https://macdoc.local/schema/" + strings.ReplaceAll(command, " ", "-") + ".json",
		"title":    "macdoc " + command + " --json",
		"type":     "object",
		"required": []string{"schema_version", "command", "generated_at", "data"},
		"properties": map[string]any{
			"schema_version": map[string]any{"const": model.SchemaVersion},
			"command":        map[string]any{"const": command},
			"generated_at":   map[string]any{"type": "string", "format": "date-time"},
			"data":           typeSchema(data, defs),
			"warnings":       map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}},
		},
		"$defs": defs,
	}
}

func typeSchema(t reflect.Type, defs map[string]any) map[string]any {
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return map[string]any{"anyOf": []any{typeSchema(t.Elem(), defs), map[string]any{"type": "null"}}}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Slice, reflect.Array:
		// Go encodes a nil slice as null.
		return map[string]any{"type": []string{"array", "null"}, "items": typeSchema(t.Elem(), defs)}
	case reflect.Map:
		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": typeSchema(t.Elem(), defs)}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Struct:
		name := t.PkgPath()[strings.LastIndex(t.PkgPath(), "/")+1:] + "." + t.Name()
		if t.Name() == "" {
			return structSchema(t, defs)
		}
		if _, ok := defs[name]; !ok {
			defs[name] = nil // placeholder: recursion
			defs[name] = structSchema(t, defs)
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	}
	panic(fmt.Sprintf("schema: unsupported type %s", t))
}

func structSchema(t reflect.Type, defs map[string]any) map[string]any {
	props := map[string]any{}
	required := []string{}
	var add func(t reflect.Type)
	add = func(t reflect.Type) {
		for i := range t.NumField() {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" || (!f.IsExported() && !f.Anonymous) {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if f.Anonymous && name == "" {
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				add(ft) // embedded fields are flattened
				continue
			}
			if name == "" {
				name = f.Name
			}
			props[name] = typeSchema(f.Type, defs)
			if !strings.Contains(opts, "omitempty") {
				required = append(required, name)
			}
		}
	}
	add(t)
	slices.Sort(required)
	return map[string]any{"type": "object", "properties": props, "required": required}
}

// --- and a small validator for what the generator emits ---

func validate(root, s map[string]any, v any, at string) []string {
	if ref, ok := s["$ref"].(string); ok {
		def := root["$defs"].(map[string]any)[strings.TrimPrefix(ref, "#/$defs/")]
		return validate(root, def.(map[string]any), v, at)
	}
	if c, ok := s["const"]; ok {
		if v != c {
			return []string{fmt.Sprintf("%s: %v, want %v", at, v, c)}
		}
		return nil
	}
	if alts, ok := s["anyOf"].([]any); ok {
		for _, alt := range alts {
			if len(validate(root, alt.(map[string]any), v, at)) == 0 {
				return nil
			}
		}
		return []string{at + ": matches no alternative"}
	}
	types := map[string]bool{}
	switch ty := s["type"].(type) {
	case string:
		types[ty] = true
	case []any:
		for _, x := range ty {
			types[x.(string)] = true
		}
	case nil:
		return nil // any
	}
	var errs []string
	switch x := v.(type) {
	case nil:
		if !types["null"] {
			errs = append(errs, at+": null")
		}
	case bool:
		if !types["boolean"] {
			errs = append(errs, at+": boolean")
		}
	case float64:
		if !types["number"] && !(types["integer"] && x == float64(int64(x))) {
			errs = append(errs, at+": number")
		}
	case string:
		if !types["string"] {
			errs = append(errs, at+": string")
		}
	case []any:
		if !types["array"] {
			return []string{at + ": array"}
		}
		for i, e := range x {
			errs = append(errs, validate(root, s["items"].(map[string]any), e, fmt.Sprintf("%s[%d]", at, i))...)
		}
	case map[string]any:
		if !types["object"] {
			return []string{at + ": object"}
		}
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, ok := x[r.(string)]; !ok {
					errs = append(errs, at+": missing "+r.(string))
				}
			}
		}
		props, _ := s["properties"].(map[string]any)
		extra, _ := s["additionalProperties"].(map[string]any)
		for k, e := range x {
			switch {
			case props[k] != nil:
				errs = append(errs, validate(root, props[k].(map[string]any), e, at+"."+k)...)
			case extra != nil:
				errs = append(errs, validate(root, extra, e, at+"."+k)...)
			case props != nil:
				errs = append(errs, at+": unexpected field "+k)
			}
		}
	}
	return errs
}

// The validator rejects output that doesn't fit, so the test above means
// something.
func TestSchemaValidatorRejects(t *testing.T) {
	type item struct {
		Name string   `json:"name"`
		Size int64    `json:"size"`
		Tags []string `json:"tags,omitempty"`
	}
	s := envelopeSchema("x", reflect.TypeOf(item{}))
	b, _ := json.Marshal(s)
	var schema map[string]any
	_ = json.Unmarshal(b, &schema)
	env := func(data string) map[string]any {
		var v map[string]any
		_ = json.Unmarshal([]byte(`{"schema_version":"1","command":"x","generated_at":"2026-10-28T00:00:00Z","data":`+data+`}`), &v)
		return v
	}
	if errs := validate(schema, schema, env(`{"name":"a","size":3}`), "$"); len(errs) != 0 {
		t.Errorf("valid rejected: %v", errs)
	}
	for _, bad := range []string{`{"name":"a"}`, `{"name":1,"size":3}`, `{"name":"a","size":1.5}`, `{"name":"a","size":3,"extra":true}`, `{"name":"a","size":3,"tags":[1]}`, `[]`} {
		if errs := validate(schema, schema, env(bad), "$"); len(errs) == 0 {
			t.Errorf("%s accepted", bad)
		}
	}
}
