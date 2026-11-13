package inventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

func init() { Register(runtimesSource{}) }

// runtimesSource lists the language runtimes that version managers keep as
// one folder per version. Those folders are the installs, so they are read
// directly; nothing is run.
type runtimesSource struct{}

// Name implements Source.
func (runtimesSource) Name() string { return "runtimes" }

// Discover implements Source. A missing folder means that manager has nothing
// installed, which is not an error.
func (runtimesSource) Discover(_ context.Context, env Env) ([]Software, error) {
	var res []Software
	res = append(res, versionFolders(filepath.Join(env.Home, ".nvm/versions/node"), "nvm", "node ")...)
	res = append(res, versionFolders(filepath.Join(env.Home, ".pyenv/versions"), "pyenv", "python ")...)
	res = append(res, versionFolders(filepath.Join(env.Home, ".rustup/toolchains"), "rustup", "rust ")...)
	for _, dir := range env.JavaDirs {
		res = append(res, jdkFolders(dir)...)
	}
	return res, nil
}

// versionFolders lists one runtime per folder under dir.
func versionFolders(dir, source, prefix string) []Software {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var res []Software
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		res = append(res, Software{ID: "runtime:" + source + ":" + e.Name(), Name: prefix + e.Name(),
			Kind: "runtime", Version: strings.TrimPrefix(e.Name(), "v"), Source: source,
			Evidence: filepath.Join(dir, e.Name())})
	}
	return res
}

// jdkFolders lists the Java development kits installed in dir, one *.jdk
// bundle each.
func jdkFolders(dir string) []Software {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var res []Software
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jdk") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".jdk")
		res = append(res, Software{ID: "runtime:jdk:" + name, Name: "Java " + name, Kind: "runtime",
			Source: "jdk", Evidence: filepath.Join(dir, e.Name())})
	}
	return res
}
