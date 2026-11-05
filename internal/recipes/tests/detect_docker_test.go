package tests

import (
	"errors"
	"macdoc/internal/recipes"
	"testing"

	"macdoc/internal/model"
	"macdoc/internal/platform"
)

const (
	dockerDFKey       = "docker system df --format {{json .}}"
	dockerDanglingKey = "docker images -f dangling=true --format {{json .}}"
)

func w2DockerEnv(t *testing.T) recipes.Env {
	t.Helper()
	w2WithTools(t, "docker")
	return recipes.Env{Runner: w2Fake(map[string]platform.Result{
		dockerDFKey:       {Stdout: w2Fixture(t, "w2_docker_system_df.txt")},
		dockerDanglingKey: {Stdout: w2Fixture(t, "w2_docker_dangling.txt")},
	})}
}

func TestDockerDF(t *testing.T) {
	env := w2DockerEnv(t)
	for id, want := range map[string]int64{
		"docker.build_cache":        3_400_000_000,
		"docker.unused_images":      1_100_000_000,
		"docker.stopped_containers": 12_500_000,
		"docker.volumes":            845_200_000, // advise: total size, not reclaimable
	} {
		fs, err := w2Run(t, id, env)
		if err != nil || len(fs) != 1 || fs[0].Bytes.Value != want || fs[0].Bytes.Source != model.SourceTool || fs[0].Note == "" || fs[0].Target != "" {
			t.Errorf("%s = %+v, %v; want %d", id, fs, err, want)
		}
	}
}

func TestDockerDFNothingReclaimable(t *testing.T) {
	w2WithTools(t, "docker")
	out := []byte(`{"Active":"1","Reclaimable":"0B (0%)","Size":"1GB","TotalCount":"1","Type":"Images"}` + "\n")
	fs, err := w2Run(t, "docker.unused_images", recipes.Env{Runner: w2Fake(map[string]platform.Result{dockerDFKey: {Stdout: out}})})
	w2WantNone(t, fs, err)
	// A type missing from the output.
	fs, err = w2Run(t, "docker.build_cache", recipes.Env{Runner: w2Fake(map[string]platform.Result{dockerDFKey: {Stdout: out}})})
	w2WantNone(t, fs, err)
}

func TestDockerDanglingImages(t *testing.T) {
	fs, err := w2Run(t, "docker.dangling_images", w2DockerEnv(t))
	if err != nil || len(fs) != 1 {
		t.Fatalf("got %+v, %v", fs, err)
	}
	if fs[0].Bytes.Value != 1_712_000_000 || fs[0].Bytes.Source != model.SourceEstimated || fs[0].Bytes.Reason == "" || fs[0].Note != "2 untagged images" {
		t.Errorf("finding %+v", fs[0])
	}
	w2WithTools(t, "docker")
	fs, err = w2Run(t, "docker.dangling_images", recipes.Env{Runner: w2Fake(map[string]platform.Result{dockerDanglingKey: {}})})
	w2WantNone(t, fs, err)
	fs, err = w2Run(t, "docker.dangling_images", recipes.Env{Runner: w2Fake(map[string]platform.Result{dockerDanglingKey: {Stdout: []byte(`{"ID":"x","Size":"N/A"}`)}})})
	if err != nil || len(fs) != 1 || fs[0].Bytes.Source != model.SourceUnknown || fs[0].Bytes.Reason == "" {
		t.Fatalf("unreadable size = %+v, %v", fs, err)
	}
}

func TestDockerDaemonDown(t *testing.T) {
	w2WithTools(t, "docker")
	down := platform.Result{ExitCode: 1, Stderr: w2Fixture(t, "docker_not_running.txt")}
	classic := platform.Result{ExitCode: 1, Stderr: []byte("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\n")}
	for _, res := range []platform.Result{down, classic} {
		env := recipes.Env{Runner: w2Fake(map[string]platform.Result{dockerDFKey: res, dockerDanglingKey: res})}
		for _, id := range []string{"docker.build_cache", "docker.unused_images", "docker.stopped_containers", "docker.volumes", "docker.dangling_images"} {
			fs, err := w2Run(t, id, env)
			w2WantNone(t, fs, err)
		}
	}
}

func TestDockerMissingAndErrors(t *testing.T) {
	w2WithTools(t)
	runner := w2Fake(nil)
	for _, id := range []string{"docker.build_cache", "docker.dangling_images"} {
		fs, err := w2Run(t, id, recipes.Env{Runner: runner})
		w2WantNone(t, fs, err)
		fs, err = w2Run(t, id, recipes.Env{})
		w2WantNone(t, fs, err)
	}
	if len(runner.Calls) != 0 {
		t.Errorf("ran %v without docker", runner.Calls)
	}
	w2WithTools(t, "docker")
	for name, res := range map[string]platform.Result{
		"other failure": {ExitCode: 1, Stderr: []byte("unknown flag: --format")},
		"bad json":      {Stdout: []byte("{not json\n")},
		"bad size":      {Stdout: []byte(`{"Type":"Build Cache","Reclaimable":"lots","Size":"1GB"}`)},
	} {
		env := recipes.Env{Runner: w2Fake(map[string]platform.Result{dockerDFKey: res, dockerDanglingKey: res})}
		if _, err := w2Run(t, "docker.build_cache", env); err == nil {
			t.Errorf("%s: docker_df accepted", name)
		}
		if name != "bad size" {
			if _, err := w2Run(t, "docker.dangling_images", env); err == nil {
				t.Errorf("%s: docker_dangling accepted", name)
			}
		}
	}
	env := recipes.Env{Runner: &platform.FakeRunner{Errors: map[string]error{dockerDFKey: errors.New("timeout")}}}
	if _, err := w2Run(t, "docker.build_cache", env); err == nil {
		t.Error("runner error swallowed")
	}
	if _, err := recipes.DetectDockerDF(t.Context(), env, recipes.Recipe{ID: "x.y"}); err == nil {
		t.Error("missing params.type accepted")
	}
}
