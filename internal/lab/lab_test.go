// SPDX-License-Identifier: Apache-2.0

package lab

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/shivangtanwar/kbark/demo"
)

// Every fixture must be something the lab is willing to apply: a known
// kind, in the demo namespace. This is what keeps the lab's write
// surface to one namespace.
func TestEveryFixtureIsAppliableByTheLab(t *testing.T) {
	entries, err := demo.Fixtures.ReadDir("fixtures")
	if err != nil || len(entries) == 0 {
		t.Fatalf("no fixtures embedded: %v", err)
	}
	for _, e := range entries {
		raw, _ := demo.Fixtures.ReadFile("fixtures/" + e.Name())
		for _, doc := range strings.Split(string(raw), "\n---") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			var obj struct {
				Kind     string `json:"kind"`
				Metadata struct {
					Name, Namespace string
				} `json:"metadata"`
			}
			if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
				t.Fatalf("%s: %v", e.Name(), err)
			}
			if _, ok := resources[obj.Kind]; !ok || obj.Metadata.Namespace != Namespace || obj.Metadata.Name == "" {
				t.Errorf("%s: %s %q in namespace %q is outside what the lab applies", e.Name(), obj.Kind, obj.Metadata.Name, obj.Metadata.Namespace)
			}
		}
	}
}

func TestScenarioCatalogueIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Scenarios() {
		if s.ID == "" || s.Title == "" || s.Breaks == "" || s.Expect == "" || s.Group == "" || s.run == nil {
			t.Errorf("scenario %q is missing a field", s.ID)
		}
		if seen[s.ID] || s.ID == Reset {
			t.Errorf("scenario id %q is duplicated or reserved", s.ID)
		}
		seen[s.ID] = true
	}
}

func TestStartRejectsUnknownTargets(t *testing.T) {
	l := &Lab{order: []string{"a"}, targets: map[string]*target{"a": {name: "a"}}, status: map[string]*Status{"a": {Cluster: "a"}}}
	if err := l.Start("nope", "oom", "x"); err != ErrUnknown {
		t.Errorf("unknown cluster: %v", err)
	}
	if err := l.Start("a", "nope", "x"); err != ErrUnknown {
		t.Errorf("unknown scenario: %v", err)
	}
	l.status["a"].Running = []Step{{ID: "oom"}}
	if err := l.Start("a", "oom", "x"); err != ErrBusy {
		t.Errorf("the same scenario twice: %v", err)
	}
	if err := l.Start("a", Reset, "x"); err != ErrBusy {
		t.Errorf("a reset while a scenario runs: %v", err)
	}
	l.status["a"].Running = []Step{{ID: Reset}}
	if err := l.Start("a", "crash", "x"); err != ErrBusy {
		t.Errorf("a scenario during a reset: %v", err)
	}
	if got := l.Statuses()[0].Running; len(got) != 1 {
		t.Errorf("statuses = %+v", got)
	}
}
