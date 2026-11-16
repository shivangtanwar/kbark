// SPDX-License-Identifier: Apache-2.0

package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultPolicy(t *testing.T) {
	p := Default([]string{"kube-system"}, "2Gi")
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if d := p.For("any", "kube-system"); !d.DetectOnly {
		t.Error("system namespace should be detect-only")
	}
	d := p.For("any", "shop")
	if d.DetectOnly || !d.Allows("rollback-workload") || d.MemoryCeiling.String() != "2Gi" || d.MaxAttempts != 2 {
		t.Errorf("unexpected default decision: %+v", d)
	}
}

func TestFileRulesFirstMatchWins(t *testing.T) {
	file := write(t, `
defaults:
  memory_ceiling: 1Gi
rules:
  - name: production
    clusters: ["prod-*"]
    namespaces: ["pay*"]
    actions: [rollback-workload]
    memory_ceiling: 4Gi
  - name: prod-rest
    clusters: ["prod-*"]
    mode: detect-only
`)
	p, err := Load(file, Default([]string{"kube-system"}, "2Gi"))
	if err != nil {
		t.Fatal(err)
	}
	pay := p.For("prod-eu", "payments")
	if pay.Rule != "production" || pay.DetectOnly || !pay.Allows("rollback-workload") || pay.Allows("set-memory-limit") || pay.MemoryCeiling.String() != "4Gi" {
		t.Errorf("payments: %+v", pay)
	}
	if d := p.For("prod-eu", "search"); !d.DetectOnly || d.Rule != "prod-rest" {
		t.Errorf("search: %+v", d)
	}
	dev := p.For("dev", "search")
	if dev.DetectOnly || dev.MemoryCeiling.String() != "1Gi" || !dev.Allows("tune-probe") || dev.MaxAttempts != 2 {
		t.Errorf("dev: %+v", dev)
	}
	if d := p.For("dev", "kube-system"); !d.DetectOnly {
		t.Error("built-in system rule must still apply after file rules")
	}
}

func TestLoadRejectsMistakes(t *testing.T) {
	for name, body := range map[string]string{
		"unknown action": "rules:\n  - actions: [exec-shell]\n",
		"bad mode":       "rules:\n  - mode: auto-apply\n",
		"bad quantity":   "defaults:\n  memory_ceiling: lots\n",
		"unknown field":  "rules:\n  - auto_approve: true\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, body), Default(nil, "2Gi"))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), "policy") {
				t.Errorf("error should name the policy file: %v", err)
			}
		})
	}
}
