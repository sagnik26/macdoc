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

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/projects"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// Checker checks preconditions. It lists processes once and reuses the
// listing for every item.
type Checker struct {
	Runner platform.Runner
	Home   string
	// AppDirs is where installed apps are looked for; nil means the usual
	// folders. Tests set it to a fixture.
	AppDirs []string

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
		return c.stopped(ctx, app+" is open; quit it first", func(cmd string) bool {
			return strings.Contains(cmd, "/"+app+".app/Contents/MacOS/")
		})
	}
	switch pc {
	case "emulator_not_running":
		return c.stopped(ctx, "an Android emulator is running; close it first", func(cmd string) bool {
			b := exe(cmd)
			return strings.HasPrefix(b, "qemu-system-") || b == "emulator"
		})
	case "simulator_not_running":
		return c.stopped(ctx, "a Simulator is running; quit Simulator first", func(cmd string) bool {
			return strings.Contains(cmd, "/Simulator.app/Contents/MacOS/Simulator") || exe(cmd) == "launchd_sim"
		})
	case "gradle_daemons_stopped":
		return c.stopped(ctx, "Gradle daemons are running; stop them with ./gradlew --stop", func(cmd string) bool {
			return strings.Contains(cmd, "org.gradle.launcher.daemon.bootstrap.GradleDaemon")
		})
	case "metro_not_running":
		return c.stopped(ctx, "Metro is running; stop the bundler first", func(cmd string) bool {
			return strings.Contains(cmd, "node") && (strings.Contains(cmd, "react-native start") || strings.Contains(cmd, "/metro/"))
		})
	case "docker_reachable":
		res, err := c.Runner.Run(ctx, platform.Command{Name: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}})
		if err != nil || res.ExitCode != 0 || strings.TrimSpace(string(res.Stdout)) == "" {
			return errors.New("the Docker daemon isn't running; start Docker Desktop first")
		}
		return nil
	case "ollama_model_not_loaded":
		return c.ollama(ctx, item)
	case "git_clean":
		return c.git(ctx, item)
	case "app_bundle_not_running":
		name := strings.TrimSuffix(filepath.Base(item.Target), ".app")
		if name == "" || name == "." {
			return errors.New("no app named")
		}
		return c.stopped(ctx, name+" is open; quit it first", func(cmd string) bool {
			return strings.Contains(cmd, "/"+name+".app/Contents/MacOS/")
		})
	case "no_installed_app":
		return c.noInstalledApp(ctx, item)
	}
	return fmt.Errorf("unknown precondition %q", pc)
}

// stopped fails with why if any process command line matches.
func (c *Checker) stopped(ctx context.Context, why string, match func(string) bool) error {
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

// ollama fails if the model the item removes is loaded (`ollama ps`).
func (c *Checker) ollama(ctx context.Context, item model.PlanItem) error {
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

// git fails if the project holding the target has uncommitted changes
// or git can't say. The project folder is the target's parent, or the one
// above an "ios" folder.
func (c *Checker) git(ctx context.Context, item model.PlanItem) error {
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

// noInstalledApp fails if the folder is named after an app that is installed
// now, or after a background item that exists now, or if that can't be told.
// It runs right before the move, so an app installed after the plan was made
// stops the item.
func (c *Checker) noInstalledApp(ctx context.Context, item model.PlanItem) error {
	id, ok := recipes.LeftoverID(filepath.Base(item.Target))
	if !ok {
		return errors.New("the folder is not named after an app")
	}
	installed, err := recipes.InstalledIDs(ctx, c.Runner, c.Home, c.AppDirs)
	if err != nil {
		return fmt.Errorf("couldn't list the installed apps: %w", err)
	}
	if recipes.OwnedByInstalled(id, installed) {
		return fmt.Errorf("%s belongs to an app or background item that is installed", id)
	}
	return nil
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
