// Package precond checks a recipe's preconditions right before apply acts
// (docs/SAFETY_MODEL.md §4.3 step 5): the owning app isn't running, a daemon
// is reachable, an emulator is stopped, a project has no uncommitted
// changes. It only reads, through platform.Runner.
package precond

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/projects"
	"macdoc/internal/recipes"
)

// Checker checks preconditions. It lists processes once and reuses the
// listing for every item.
type Checker struct {
	Runner platform.Runner
	Home   string

	once  sync.Once
	procs []string
	err   error
}

// Check returns nil if every precondition of item holds, or the first one
// that doesn't, as a reason a person can act on. An unknown precondition or
// one that can't be checked fails: apply never assumes.
func (c *Checker) Check(ctx context.Context, item model.PlanItem) error {
	for _, pc := range item.Preconditions {
		if err := c.check(ctx, pc, item); err != nil {
			return err
		}
	}
	return nil
}

func (c *Checker) check(ctx context.Context, pc string, item model.PlanItem) error {
	if c.Runner == nil {
		return errors.New("preconditions can't be checked")
	}
	if app, ok := strings.CutPrefix(pc, "app_not_running:"); ok {
		return c.notRunning(ctx, app+" is open; quit it first", func(cmd string) bool {
			return strings.Contains(cmd, "/"+app+".app/Contents/MacOS/")
		})
	}
	switch pc {
	case "emulator_not_running":
		return c.notRunning(ctx, "an Android emulator is running; close it first", func(cmd string) bool {
			b := exe(cmd)
			return strings.HasPrefix(b, "qemu-system-") || b == "emulator"
		})
	case "simulator_not_running":
		return c.notRunning(ctx, "a Simulator is running; quit Simulator first", func(cmd string) bool {
			return strings.Contains(cmd, "/Simulator.app/Contents/MacOS/Simulator") || exe(cmd) == "launchd_sim"
		})
	case "gradle_daemons_stopped":
		return c.notRunning(ctx, "Gradle daemons are running; stop them with ./gradlew --stop", func(cmd string) bool {
			return strings.Contains(cmd, "org.gradle.launcher.daemon.bootstrap.GradleDaemon")
		})
	case "metro_not_running":
		return c.notRunning(ctx, "Metro is running; stop the bundler first", func(cmd string) bool {
			return strings.Contains(cmd, "node") && (strings.Contains(cmd, "react-native start") || strings.Contains(cmd, "/metro/"))
		})
	case "docker_reachable":
		res, err := c.Runner.Run(ctx, platform.Command{Name: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}})
		if err != nil || res.ExitCode != 0 || strings.TrimSpace(string(res.Stdout)) == "" {
			return errors.New("the Docker daemon isn't running; start Docker Desktop first")
		}
		return nil
	case "ollama_model_not_loaded":
		return c.ollamaIdle(ctx, item)
	case "git_clean":
		return c.gitClean(ctx, item)
	}
	return fmt.Errorf("unknown precondition %q", pc)
}

// notRunning fails with why if any process command line matches.
func (c *Checker) notRunning(ctx context.Context, why string, match func(string) bool) error {
	procs, err := c.processes(ctx)
	if err != nil {
		return fmt.Errorf("couldn't list running programs: %w", err)
	}
	for _, p := range procs {
		if match(p) {
			return errors.New(why)
		}
	}
	return nil
}

func (c *Checker) processes(ctx context.Context) ([]string, error) {
	c.once.Do(func() {
		res, err := c.Runner.Run(ctx, platform.Command{Name: "ps", Args: []string{"-axww", "-o", "command="}})
		switch {
		case err != nil:
			c.err = err
		case res.ExitCode != 0:
			c.err = fmt.Errorf("ps exited %d", res.ExitCode)
		default:
			for _, l := range strings.Split(string(res.Stdout), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					c.procs = append(c.procs, l)
				}
			}
		}
	})
	return c.procs, c.err
}

// exe is the base name of a command line's program (up to the first space;
// app bundle paths with spaces are matched by the callers directly).
func exe(cmd string) string {
	first, _, _ := strings.Cut(cmd, " ")
	return filepath.Base(first)
}

// ollamaIdle fails if the model the item removes is loaded (`ollama ps`).
func (c *Checker) ollamaIdle(ctx context.Context, item model.PlanItem) error {
	if len(item.Argv) == 0 {
		return errors.New("no model named")
	}
	name := item.Argv[len(item.Argv)-1]
	res, err := c.Runner.Run(ctx, platform.Command{Name: "ollama", Args: []string{"ps"}})
	if err != nil || res.ExitCode != 0 {
		return errors.New("couldn't ask ollama which models are loaded")
	}
	for i, l := range strings.Split(string(res.Stdout), "\n") {
		f := strings.Fields(l)
		if i == 0 || len(f) == 0 {
			continue
		}
		if f[0] == name || strings.TrimSuffix(f[0], ":latest") == strings.TrimSuffix(name, ":latest") {
			return fmt.Errorf("%s is loaded in ollama; stop it with ollama stop %s", name, name)
		}
	}
	return nil
}

// gitClean fails if the project holding the target has uncommitted changes
// or git can't say. The project folder is the target's parent, or the one
// above an "ios" folder.
func (c *Checker) gitClean(ctx context.Context, item model.PlanItem) error {
	if item.Target == "" {
		return errors.New("no project to check")
	}
	dir := filepath.Dir(item.Target)
	if filepath.Base(dir) == "ios" || filepath.Base(dir) == "android" {
		dir = filepath.Dir(dir)
	}
	st, note := projects.GitStatus(ctx, projects.Env{Home: c.Home, Runner: c.Runner}, dir)
	switch st {
	case projects.GitClean, projects.GitNone:
		return nil
	case projects.GitDirty:
		return errors.New("the project has uncommitted changes; commit them first")
	}
	return fmt.Errorf("couldn't check the project's git state (%s)", note)
}

// Known reports whether every precondition name is one Check understands.
func Known(pcs []string) bool {
	for _, pc := range pcs {
		ok := false
		for _, p := range recipes.Preconditions {
			if strings.HasSuffix(p, ":") {
				ok = ok || (strings.HasPrefix(pc, p) && len(pc) > len(p))
			} else if pc == p {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
