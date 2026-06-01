// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shivangtanwar/kbark/internal/config"
)

func TestLoad_emptyPathReturnsBuiltinDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if cfg.DefaultProfile != config.DefaultProfileName {
		t.Errorf("DefaultProfile = %q, want %q", cfg.DefaultProfile, config.DefaultProfileName)
	}
	dev, err := cfg.Resolve("dev")
	if err != nil {
		t.Fatalf("Resolve(dev): %v", err)
	}
	if dev.Provider != "anthropic" || dev.Model == "" {
		t.Errorf("default dev profile = %+v, want anthropic+model", dev)
	}
	if !dev.TranscriptsEnabled() {
		t.Error("default dev profile should have transcripts on")
	}
}

func TestLoad_missingFileFallsBackToDefaults(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load(missing): %v", err)
	}
	if _, err := cfg.Resolve("dev"); err != nil {
		t.Errorf("expected default dev profile to exist: %v", err)
	}
}

func TestLoad_parsesValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
default_profile: prod
profiles:
  dev:
    provider: anthropic
    model: claude-sonnet-4-6
    transcripts: on
  prod:
    provider: openai
    model: gpt-4o
    transcripts: off
`
	if err := writeFile(path, body); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultProfile != "prod" {
		t.Errorf("DefaultProfile = %q, want %q", cfg.DefaultProfile, "prod")
	}

	prod, err := cfg.Resolve("prod")
	if err != nil {
		t.Fatalf("Resolve(prod): %v", err)
	}
	if prod.Provider != "openai" || prod.Model != "gpt-4o" {
		t.Errorf("prod = %+v", prod)
	}
	if prod.TranscriptsEnabled() {
		t.Error("prod profile should have transcripts off")
	}

	if _, err := cfg.Resolve(""); err != nil {
		t.Errorf("Resolve(\"\") should fall back to default_profile: %v", err)
	}
}

// TestLoad_singleProfileAutoDefaults pins the convenience path: a
// config file with one profile and no default_profile auto-uses
// that profile as default.
func TestLoad_singleProfileAutoDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single.yaml")
	body := `
profiles:
  only:
    provider: anthropic
    model: claude-opus-4-7
`
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultProfile != "only" {
		t.Errorf("DefaultProfile = %q, want %q (auto-pick)", cfg.DefaultProfile, "only")
	}
}

func TestLoad_emptyProfilesIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.yaml")
	if err := writeFile(path, "profiles: {}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Error("expected error on empty profiles map")
	}
}

func TestLoad_malformedYAMLIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := writeFile(path, "this is: not :: valid: yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Error("expected error on malformed YAML")
	}
}

// TestLoad_unknownDefaultProfileIsError pins the helpful failure:
// a typo in default_profile is caught at load time rather than
// failing later with a confusing "profile not found" at Resolve.
func TestLoad_unknownDefaultProfileIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.yaml")
	body := `
default_profile: typo
profiles:
  dev:
    provider: anthropic
    model: claude-sonnet-4-6
`
	if err := writeFile(path, body); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error when default_profile is unknown")
	}
	if !strings.Contains(err.Error(), "typo") {
		t.Errorf("error should name the missing profile, got: %v", err)
	}
}

// TestResolve_unknownProfileListsAvailable pins the discoverability
// behaviour — the user sees what they could have typed instead of
// a bare "not found" error.
func TestResolve_unknownProfileListsAvailable(t *testing.T) {
	cfg, _ := config.Load("")
	_, err := cfg.Resolve("staging")
	if err == nil {
		t.Fatal("expected error for unknown profile")
	}
	if !strings.Contains(err.Error(), "dev") {
		t.Errorf("error should list available profile names, got: %v", err)
	}
}

// TestResolve_missingProviderOrModelIsError pins required-field
// validation — a malformed profile file (provider or model blank)
// should fail at Resolve with a useful message, not silently
// produce a broken Model construction.
func TestResolve_missingProviderOrModelIsError(t *testing.T) {
	cfg := &config.Config{
		DefaultProfile: "broken",
		Profiles: map[string]config.Profile{
			"broken": {Provider: "anthropic"}, // no model
		},
	}
	if _, err := cfg.Resolve("broken"); err == nil {
		t.Error("expected error on profile missing model")
	}
}

func writeFile(path, body string) error {
	return writeFileBytes(path, []byte(body))
}
