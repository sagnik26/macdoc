package tests

import (
	"errors"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/recipes"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

const ollamaKey = "ollama list"

func TestOllamaModels(t *testing.T) {
	w2WithTools(t, "ollama")
	fs, err := w2Run(t, "ollama.model", recipes.Env{Runner: w2Fake(map[string]platform.Result{ollamaKey: {Stdout: w2Fixture(t, "w2_ollama_list.txt")}})})
	if err != nil {
		t.Fatal(err)
	}
	// someuser/custom and hf.co/... contain "/" and are skipped.
	want := []struct {
		arg   string
		bytes int64
		last  time.Time
	}{
		// "3 weeks ago" is parsed relative to recipes.Env.Now since P3-W4 D15.
		{"llama3.2:latest", 2_000_000_000, w2Now.Add(-21 * 24 * time.Hour)},
		{"qwen2.5-coder:7b", 4_700_000_000, time.Date(2026, 5, 2, 14, 3, 11, 0, time.UTC)},
		{"nomic-embed-text:latest", 274_000_000, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)},
	}
	if len(fs) != len(want) {
		t.Fatalf("got %+v", fs)
	}
	for i, w := range want {
		f := fs[i]
		if f.NativeArg != w.arg || f.Bytes.Value != w.bytes || f.Bytes.Source != model.SourceTool || !f.LastUsed.Equal(w.last) || f.Target != "" {
			t.Errorf("finding %d = %+v, want %+v", i, f, w)
		}
	}
	if fs[0].Note != "" || fs[1].Note != "" {
		t.Errorf("notes %q %q", fs[0].Note, fs[1].Note)
	}
}

func TestOllamaRejectsUnsafeNames(t *testing.T) {
	w2WithTools(t, "ollama")
	out := "NAME ID SIZE MODIFIED\n" +
		"-rf:latest 1 1 GB now\n" +
		"a;b:latest 1 1 GB now\n" +
		"a$b 1 1 GB now\n" +
		"../x:1 1 1 GB now\n" +
		"x:y:z 1 1 GB now\n" +
		"ok:1 abc 1.5GB 2026-01-02\n" +
		"odd:1 abc ??? now\n" +
		"short line\n"
	fs, err := w2Run(t, "ollama.model", recipes.Env{Runner: w2Fake(map[string]platform.Result{ollamaKey: {Stdout: []byte(out)}})})
	if err != nil || len(fs) != 2 || fs[0].NativeArg != "ok:1" || fs[0].Bytes.Value != 1_500_000_000 || fs[0].LastUsed.IsZero() {
		t.Fatalf("got %+v, %v", fs, err)
	}
	if fs[1].NativeArg != "odd:1" || fs[1].Bytes.Source != model.SourceUnknown || fs[1].Bytes.Reason == "" {
		t.Errorf("unknown size finding %+v", fs[1])
	}
}

func TestOllamaMissingOrDown(t *testing.T) {
	w2WithTools(t)
	runner := w2Fake(nil)
	fs, err := w2Run(t, "ollama.model", recipes.Env{Runner: runner})
	w2WantNone(t, fs, err)
	if len(runner.Calls) != 0 {
		t.Errorf("ran %v without ollama", runner.Calls)
	}
	w2WithTools(t, "ollama")
	fs, err = w2Run(t, "ollama.model", recipes.Env{Runner: w2Fake(map[string]platform.Result{ollamaKey: {ExitCode: 1, Stderr: w2Fixture(t, "w2_ollama_not_running.txt")}})})
	w2WantNone(t, fs, err)
	fs, err = w2Run(t, "ollama.model", recipes.Env{Runner: w2Fake(map[string]platform.Result{ollamaKey: {Stdout: []byte("NAME ID SIZE MODIFIED\n")}})})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "ollama.model", recipes.Env{Runner: w2Fake(map[string]platform.Result{ollamaKey: {ExitCode: 2, Stderr: []byte("Error: unknown")}})}); err == nil {
		t.Error("other failure accepted")
	}
	if _, err := w2Run(t, "ollama.model", recipes.Env{Runner: &platform.Fake{Errors: map[string]error{ollamaKey: errors.New("timeout")}}}); err == nil {
		t.Error("runner error swallowed")
	}
}

func TestHFCacheRepo(t *testing.T) {
	hub := w2Home + "/.cache/huggingface/hub"
	nodes := fakeNodes{
		{Path: hub, Bytes: 5 << 30},
		{Path: hub + "/models--meta-llama--Llama-3-8B", Bytes: 5 << 30, MtimeMax: w2DaysAgo(90)},
		{Path: hub + "/models--meta-llama--Llama-3-8B/blobs", Bytes: 5 << 30},
	}
	fs, err := w2Run(t, "hf.cache_repo", recipes.Env{Nodes: nodes})
	w2WantTargets(t, fs, err, hub+"/models--meta-llama--Llama-3-8B")
}

func TestLMStudioModels(t *testing.T) {
	a := w2Home + "/.lmstudio/models"
	b := w2Home + "/.cache/lm-studio/models"
	nodes := fakeNodes{
		{Path: a, Bytes: 9 << 30},
		{Path: a + "/lmstudio-community", Bytes: 9 << 30}, // publisher
		{Path: a + "/lmstudio-community/Qwen2.5-7B-GGUF", Bytes: 5 << 30, MtimeMax: w2DaysAgo(70)}, // model
		{Path: a + "/lmstudio-community/Qwen2.5-7B-GGUF/q4.gguf", Kind: model.NodeFile, Bytes: 5 << 30},
		{Path: a + "/lmstudio-community/stray.gguf", Kind: model.NodeFile, Bytes: 1 << 30}, // file, not a model dir
		{Path: a + "/lmstudio-community/empty", Bytes: 0},
		{Path: b + "/TheBloke", Bytes: 4 << 30},
		{Path: b + "/TheBloke/Mistral-7B-GGUF", Bytes: 4 << 30, MtimeMax: w2DaysAgo(10)},
	}
	fs, err := w2Run(t, "lmstudio.model", recipes.Env{Nodes: nodes})
	w2WantTargets(t, fs, err, a+"/lmstudio-community/Qwen2.5-7B-GGUF", b+"/TheBloke/Mistral-7B-GGUF")
	for _, f := range fs {
		if f.Title == "" || f.Bytes.Source != model.SourceMeasured {
			t.Errorf("finding %+v", f)
		}
	}
	fs, err = w2Run(t, "lmstudio.model", recipes.Env{})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "lmstudio.model", recipes.Env{Nodes: w2ErrNodes{}}); err == nil {
		t.Error("Nodes error swallowed")
	}
	if _, err := recipes.DetectLMStudioModels(t.Context(), recipes.Env{Nodes: fakeNodes{}}, recipes.Recipe{ID: "x.y"}); err == nil {
		t.Error("missing params.dirs accepted")
	}
}
