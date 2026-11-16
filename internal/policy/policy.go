// SPDX-License-Identifier: Apache-2.0

// Package policy decides, per cluster and namespace, what the autofix
// layer may propose. Policy narrows what kbark offers; it never widens
// the fixed set of actions, and it never removes the approval step.
package policy

import (
	"fmt"
	"os"
	"path"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

// ModeDetectOnly rules make kbark report failures without proposing changes.
const (
	ModePropose    = "propose"
	ModeDetectOnly = "detect-only"
)

// Rule is one entry of the policy file. The first rule whose clusters
// and namespaces both match applies; empty lists match everything.
// Patterns are shell globs ("prod-*").
type Rule struct {
	Name       string   `json:"name,omitempty"`
	Clusters   []string `json:"clusters,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
	// Mode is "propose" (default) or "detect-only".
	Mode string `json:"mode,omitempty"`
	// Actions, when set, is the allowlist of action types kbark may
	// propose under this rule. Empty means all of them.
	Actions []string `json:"actions,omitempty"`
	// MemoryCeiling overrides the default for this rule.
	MemoryCeiling string `json:"memory_ceiling,omitempty"`
}

// Defaults apply where a rule does not override them.
type Defaults struct {
	MemoryCeiling string `json:"memory_ceiling,omitempty"`
	MaxAttempts   int    `json:"max_attempts,omitempty"`
	AttemptWindow string `json:"attempt_window,omitempty"`
}

// Policy is the parsed file.
type Policy struct {
	Defaults Defaults `json:"defaults"`
	Rules    []Rule   `json:"rules"`
	// Source is where the policy came from, for display.
	Source string `json:"source"`
}

// Decision is the policy outcome for one cluster and namespace.
type Decision struct {
	// Rule names the matching rule, or "" when only defaults applied.
	Rule          string
	DetectOnly    bool
	actions       map[string]bool
	MemoryCeiling resource.Quantity
	MaxAttempts   int
	AttemptWindow time.Duration
}

// Allows reports whether an action type may be proposed.
func (d Decision) Allows(action string) bool { return d.actions == nil || d.actions[action] }

// KnownActions is the closed set of action types; Load rejects others.
var KnownActions = []string{
	"set-memory-limit", "rollback-workload", "tune-probe", "delete-pod", "restart-pod", "uncordon-node",
	"set-ephemeral-storage-limit", "set-image-pull-policy", "fix-service-selector",
}

// Default builds the built-in policy: the given namespaces are
// detect-only, everything else may be proposed.
func Default(detectOnly []string, memoryCeiling string) *Policy {
	p := &Policy{Defaults: Defaults{MemoryCeiling: memoryCeiling, MaxAttempts: 2, AttemptWindow: "1h"}, Source: "built-in defaults"}
	if len(detectOnly) > 0 {
		p.Rules = append(p.Rules, Rule{Name: "system namespaces", Namespaces: detectOnly, Mode: ModeDetectOnly})
	}
	return p
}

// Load reads and validates a policy file. The base policy supplies
// defaults the file leaves unset; its rules are appended after the
// file's, so system namespaces stay detect-only unless the file says
// otherwise with an earlier rule.
func Load(file string, base *Policy) (*Policy, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	var p Policy
	if err := yaml.UnmarshalStrict(raw, &p); err != nil {
		return nil, fmt.Errorf("policy %s: %w", file, err)
	}
	if p.Defaults.MemoryCeiling == "" {
		p.Defaults.MemoryCeiling = base.Defaults.MemoryCeiling
	}
	if p.Defaults.MaxAttempts == 0 {
		p.Defaults.MaxAttempts = base.Defaults.MaxAttempts
	}
	if p.Defaults.AttemptWindow == "" {
		p.Defaults.AttemptWindow = base.Defaults.AttemptWindow
	}
	p.Rules = append(p.Rules, base.Rules...)
	p.Source = file
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("policy %s: %w", file, err)
	}
	return &p, nil
}

// Validate checks every value a Decision would later parse.
func (p *Policy) Validate() error {
	if _, err := resource.ParseQuantity(p.Defaults.MemoryCeiling); err != nil {
		return fmt.Errorf("defaults.memory_ceiling: %w", err)
	}
	if _, err := time.ParseDuration(p.Defaults.AttemptWindow); err != nil {
		return fmt.Errorf("defaults.attempt_window: %w", err)
	}
	if p.Defaults.MaxAttempts < 1 {
		return fmt.Errorf("defaults.max_attempts must be at least 1")
	}
	known := map[string]bool{}
	for _, a := range KnownActions {
		known[a] = true
	}
	for i, r := range p.Rules {
		where := fmt.Sprintf("rules[%d]", i)
		if r.Mode != "" && r.Mode != ModePropose && r.Mode != ModeDetectOnly {
			return fmt.Errorf("%s.mode %q: want %q or %q", where, r.Mode, ModePropose, ModeDetectOnly)
		}
		for _, a := range r.Actions {
			if !known[a] {
				return fmt.Errorf("%s.actions: unknown action %q", where, a)
			}
		}
		if r.MemoryCeiling != "" {
			if _, err := resource.ParseQuantity(r.MemoryCeiling); err != nil {
				return fmt.Errorf("%s.memory_ceiling: %w", where, err)
			}
		}
		for _, pat := range append(append([]string{}, r.Clusters...), r.Namespaces...) {
			if _, err := path.Match(pat, "x"); err != nil {
				return fmt.Errorf("%s: bad pattern %q", where, pat)
			}
		}
	}
	return nil
}

// For returns the decision for a cluster and namespace. The policy must
// have passed Validate.
func (p *Policy) For(cluster, namespace string) Decision {
	d := Decision{MaxAttempts: p.Defaults.MaxAttempts}
	d.MemoryCeiling = resource.MustParse(p.Defaults.MemoryCeiling)
	d.AttemptWindow, _ = time.ParseDuration(p.Defaults.AttemptWindow)
	for i, r := range p.Rules {
		if !matches(r.Clusters, cluster) || !matches(r.Namespaces, namespace) {
			continue
		}
		d.Rule = r.Name
		if d.Rule == "" {
			d.Rule = fmt.Sprintf("rule %d", i+1)
		}
		d.DetectOnly = r.Mode == ModeDetectOnly
		if len(r.Actions) > 0 {
			d.actions = map[string]bool{}
			for _, a := range r.Actions {
				d.actions[a] = true
			}
		}
		if r.MemoryCeiling != "" {
			d.MemoryCeiling = resource.MustParse(r.MemoryCeiling)
		}
		break
	}
	return d
}

func matches(patterns []string, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pat := range patterns {
		if ok, _ := path.Match(pat, value); ok {
			return true
		}
	}
	return false
}
