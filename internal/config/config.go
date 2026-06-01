// SPDX-License-Identifier: Apache-2.0

// Package config loads kbark's runtime configuration from
// ~/.config/kbark/config.yaml (or the platform equivalent). The
// config is purely a static description of named profiles — which
// AI provider and model each profile uses, whether transcripts are
// saved, and so on. Profile selection happens via the --profile
// flag at startup; future M8 sub-PRs add mid-session switching.
//
// Missing or empty config is not an error: kbark ships sensible
// built-in defaults so the first run "just works" without the user
// authoring a config file. Malformed YAML or an unknown profile
// reference, however, fails fast with a useful error.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// FileName is the on-disk config filename. Lives under UserConfigDir,
// so Linux: ~/.config/kbark/config.yaml, macOS: ~/Library/Application
// Support/kbark/config.yaml, Windows: %AppData%/kbark/config.yaml.
const FileName = "config.yaml"

// DefaultProfileName is the profile chosen when neither --profile nor
// KBARK_PROFILE nor the config's default_profile is set.
const DefaultProfileName = "dev"

// Config is the parsed YAML.
type Config struct {
	DefaultProfile string             `json:"default_profile,omitempty"`
	Profiles       map[string]Profile `json:"profiles,omitempty"`
}

// Profile is one named configuration block.
type Profile struct {
	// Provider is one of "anthropic", "openai", "ollama".
	Provider string `json:"provider"`
	// Model is the provider-specific model identifier (e.g.
	// "claude-sonnet-4-6", "gpt-4o-mini", "llama3.2").
	Model string `json:"model"`
	// Transcripts toggles diagnosis transcript saving. Accepted
	// values: "on" (default), "off". The env var KBARK_TRANSCRIPTS
	// still takes precedence at runtime.
	Transcripts string `json:"transcripts,omitempty"`
	// TokenBudget caps the estimated tokens for the
	// payload+system-prompt of any single diagnose session. A
	// payload that exceeds the budget aborts before any tokens
	// are sent. 0 means unbounded (the default — kbark's payloads
	// are typically a few KB and the budget is a guard, not a
	// throttle).
	TokenBudget int `json:"token_budget,omitempty"`
}

// TranscriptsEnabled reports whether transcripts should save under
// this profile. Empty == on (the safe default).
func (p Profile) TranscriptsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(p.Transcripts))
	switch v {
	case "", "on", "true", "yes", "1":
		return true
	default:
		return false
	}
}

// DefaultPath is the platform-appropriate config file path. May
// fail on truly exotic platforms; callers should treat that as
// "config file unavailable, use built-in defaults".
func DefaultPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(base, "kbark", FileName), nil
}

// builtinDefaults is the in-memory config used when no file is found.
// Matches the v1 spec: one "dev" profile pointing at Anthropic + the
// default Sonnet model, transcripts on.
func builtinDefaults() *Config {
	return &Config{
		DefaultProfile: DefaultProfileName,
		Profiles: map[string]Profile{
			DefaultProfileName: {
				Provider:    "anthropic",
				Model:       "claude-sonnet-4-6",
				Transcripts: "on",
			},
		},
	}
}

// Load reads the config file at `path`. Returns the built-in defaults
// when path is empty or the file doesn't exist. Returns an error on
// malformed YAML or when the parsed config has no profiles (a config
// file should not be empty if it exists).
func Load(path string) (*Config, error) {
	if path == "" {
		return builtinDefaults(), nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return builtinDefaults(), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Profiles) == 0 {
		return nil, fmt.Errorf("%s defines no profiles", path)
	}
	if cfg.DefaultProfile == "" {
		// Auto-pick the first profile alphabetically as the default
		// so a user can author a single-profile config without the
		// boilerplate.
		cfg.DefaultProfile = firstSortedKey(cfg.Profiles)
	}
	if _, ok := cfg.Profiles[cfg.DefaultProfile]; !ok {
		return nil, fmt.Errorf("%s default_profile %q is not defined", path, cfg.DefaultProfile)
	}
	return &cfg, nil
}

// Resolve returns the named profile. Empty `name` falls back to the
// config's DefaultProfile. Unknown names produce an error that lists
// the available profile names so the user can correct their --profile
// flag without grepping the config.
func (c *Config) Resolve(name string) (Profile, error) {
	if name == "" {
		name = c.DefaultProfile
	}
	p, ok := c.Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("unknown profile %q (available: %s)", name, strings.Join(c.profileNames(), ", "))
	}
	if p.Provider == "" {
		return Profile{}, fmt.Errorf("profile %q is missing required field 'provider'", name)
	}
	if p.Model == "" {
		return Profile{}, fmt.Errorf("profile %q is missing required field 'model'", name)
	}
	return p, nil
}

func (c *Config) profileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for k := range c.Profiles {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func firstSortedKey(m map[string]Profile) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}
