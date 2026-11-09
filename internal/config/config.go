// Package config reads and writes macdoc's settings file. Settings can only
// make macdoc stricter (more protected folders) or change timings within
// fixed bounds; nothing here can loosen a safety rule.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/safety"
)

// Config is the settings file. Zero values mean the default.
type Config struct {
	// Protect lists extra folders macdoc must never touch, as absolute or
	// "~/" paths. They add to the built-in protected paths.
	Protect []string `json:"protect,omitempty"`
	// Plan is how long a sealed plan stays valid.
	Plan int `json:"plan_minutes,omitempty"`
	// Native bounds a tool's own cleanup command.
	Native int `json:"native_timeout_minutes,omitempty"`
}

// Setting describes one number setting.
type Setting struct {
	Key      string
	Default  int
	Min, Max int
	Help     string
}

// Settings are the number settings, in display order.
var Settings = []Setting{
	{"plan_minutes", 30, 5, 120, "how long a sealed plan stays valid"},
	{"native_timeout_minutes", 15, 1, 60, "time limit for a tool's own cleanup command"},
}

// Path is the settings file, next to macdoc's database.
func Path(home string) string {
	return filepath.Join(home, "Library", "Application Support", "macdoc", "config.json")
}

// Load reads the settings. A missing file is the defaults. A file that
// can't be read or is invalid is an error, so a protected folder is never
// silently dropped.
func Load(home string) (Config, error) {
	b, err := os.ReadFile(Path(home))
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("config %s: %w (fix or remove the file)", Path(home), err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", Path(home), err)
	}
	return c, nil
}

// Save writes the settings (owner-only), after validating them.
func Save(home string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	p := Path(home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304: macdoc's own settings file.
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("config: %w", err)
	}
	return f.Close()
}

// Validate checks every setting.
func (c Config) Validate() error {
	for _, s := range Settings {
		if v := c.Get(s.Key); v != 0 && (v < s.Min || v > s.Max) {
			return fmt.Errorf("%s must be between %d and %d", s.Key, s.Min, s.Max)
		}
	}
	for _, p := range c.Protect {
		if err := verify(p); err != nil {
			return err
		}
	}
	return nil
}

// Get returns a number setting's value (0 if unset or unknown).
func (c Config) Get(key string) int {
	switch key {
	case "plan_minutes":
		return c.Plan
	case "native_timeout_minutes":
		return c.Native
	}
	return 0
}

// Set sets a number setting; 0 restores the default.
func (c *Config) Set(key string, v int) error {
	i := slices.IndexFunc(Settings, func(s Setting) bool { return s.Key == key })
	if i < 0 {
		return fmt.Errorf("unknown setting %q", key)
	}
	if s := Settings[i]; v != 0 && (v < s.Min || v > s.Max) {
		return fmt.Errorf("%s must be between %d and %d", key, s.Min, s.Max)
	}
	switch key {
	case "plan_minutes":
		c.Plan = v
	case "native_timeout_minutes":
		c.Native = v
	}
	return nil
}

// Effective is a number setting's value, or its default when unset.
func (c Config) Effective(key string) int {
	if v := c.Get(key); v != 0 {
		return v
	}
	for _, s := range Settings {
		if s.Key == key {
			return s.Default
		}
	}
	return 0
}

// TTL is how long a sealed plan stays valid.
func (c Config) TTL() time.Duration {
	return time.Duration(c.Effective("plan_minutes")) * time.Minute
}

// Timeout bounds a tool's own cleanup command.
func (c Config) Timeout() time.Duration {
	return time.Duration(c.Effective("native_timeout_minutes")) * time.Minute
}

// AddProtect adds a protected folder; adding one twice is a no-op.
func (c *Config) AddProtect(p string) error {
	// Checked before cleaning, which would resolve ".." lexically.
	if err := verify(strings.TrimSpace(p)); err != nil {
		return err
	}
	p = tidy(p)
	if !slices.Contains(c.Protect, p) {
		c.Protect = append(c.Protect, p)
	}
	return nil
}

// RemoveProtect removes a folder added with AddProtect. Built-in protected
// paths aren't in the file, so they can't be removed.
func (c *Config) RemoveProtect(p string) error {
	p = tidy(p)
	i := slices.Index(c.Protect, p)
	if i < 0 {
		return fmt.Errorf("%s isn't in your protected list (built-in protected paths can't be removed)", p)
	}
	c.Protect = slices.Delete(c.Protect, i, i+1)
	return nil
}

func tidy(p string) string {
	p = strings.TrimSpace(p)
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return "~/" + filepath.Clean(rest)
	}
	if strings.HasPrefix(p, "/") {
		return filepath.Clean(p)
	}
	return p
}

func verify(p string) error {
	switch {
	case p == "" || p == "~" || p == "~/":
		return errors.New("a protected folder can't be empty or your whole home folder")
	case !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/"):
		return fmt.Errorf("protected folder %q must start with / or ~/", p)
	case strings.Contains(p, "\x00"):
		return errors.New("protected folder contains a NUL byte")
	}
	for _, c := range strings.Split(p, "/") {
		if c == ".." {
			return fmt.Errorf("protected folder %q must not contain a .. component", p)
		}
	}
	return nil
}

// Policy is the safety policy for home with the configured protected
// folders added. An unreadable or invalid settings file is an error.
func Policy(home string) (*safety.Policy, Config, error) {
	c, err := Load(home)
	if err != nil {
		return nil, c, err
	}
	p, err := safety.NewPolicy(home, c.Protect)
	return p, c, err
}
