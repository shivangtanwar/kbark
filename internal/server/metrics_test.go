// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shivangtanwar/kbark/internal/detect"
	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
)

func TestMetricsTextForASmallWorld(t *testing.T) {
	store := incident.NewStore(nil, nil)
	t0 := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	// Cluster "dev": one crash incident still open, one resolved.
	ref := fleet.Ref{Kind: "Deployment", Name: "api"}
	open := fleet.Observation{
		Key: fleet.Key("ns", ref, detect.FamilyCrash), Namespace: "ns", Workload: ref, Pods: []string{"api-1"},
		Finding: detect.Finding{Family: detect.FamilyCrash, Reason: "r", Summary: "s", Evidence: []detect.Evidence{{Label: "a", Value: "b"}}},
	}
	created := store.Observe("dev", []fleet.Observation{open}, t0)
	// The open one gets a proposal and a ready explanation.
	_, _ = store.Update(created[0].ID, t0, func(i *incident.Incident) (incident.State, string, string, error) {
		i.Action = &incident.Action{Type: incident.ActionRollbackWorkload, Title: "t", TargetKind: "Deployment", TargetName: "api"}
		i.ExplanationState = incident.ExplainReady
		return incident.StateProposed, "kbark", "t", nil
	})
	// A closed incident in another cluster: the failure went away
	// without a fix. (A nil scan of "dev" would close the open one too.)
	store.Observe("staging", []fleet.Observation{{
		Key: fleet.Key("ns", fleet.Ref{Kind: "Deployment", Name: "web"}, detect.FamilyOOM), Namespace: "ns",
		Workload: fleet.Ref{Kind: "Deployment", Name: "web"},
		Finding:  detect.Finding{Family: detect.FamilyOOM, Summary: "s"},
	}}, t0)
	store.Observe("staging", nil, t0.Add(time.Hour))

	snaps := []fleet.Snapshot{
		{
			Summary: fleet.Summary{
				Name: "dev", State: "connected", Nodes: 3, NodesReady: 2, Cordoned: 1,
				Pods: 7, Healthy: 5, Failing: 1, Pending: 1,
			},
		},
		{Summary: fleet.Summary{Name: "staging", State: "connecting"}},
	}

	var b strings.Builder
	writeMetrics(&b, MetricsConfig{Version: "1.2.3"}, snaps, store.List())
	got := b.String()

	want := strings.Join([]string{
		"# HELP kbark_build_info Build information, constant 1.",
		"# TYPE kbark_build_info gauge",
		`kbark_build_info{version="1.2.3"} 1`,
		"# HELP kbark_cluster_connected Whether the cluster's caches are warm (1) or not (0).",
		"# TYPE kbark_cluster_connected gauge",
		`kbark_cluster_connected{cluster="dev"} 1`,
		`kbark_cluster_connected{cluster="staging"} 0`,
		"# HELP kbark_pods Pods by health.",
		"# TYPE kbark_pods gauge",
		`kbark_pods{cluster="dev",health="healthy"} 5`,
		`kbark_pods{cluster="dev",health="failing"} 1`,
		`kbark_pods{cluster="dev",health="pending"} 1`,
		`kbark_pods{cluster="staging",health="healthy"} 0`,
		`kbark_pods{cluster="staging",health="failing"} 0`,
		`kbark_pods{cluster="staging",health="pending"} 0`,
		"# HELP kbark_nodes Nodes by state.",
		"# TYPE kbark_nodes gauge",
		`kbark_nodes{cluster="dev",state="ready"} 2`,
		`kbark_nodes{cluster="dev",state="not_ready"} 1`,
		`kbark_nodes{cluster="dev",state="cordoned"} 1`,
		`kbark_nodes{cluster="staging",state="ready"} 0`,
		`kbark_nodes{cluster="staging",state="not_ready"} 0`,
		`kbark_nodes{cluster="staging",state="cordoned"} 0`,
		"# HELP kbark_incidents Incidents by lifecycle state, closed ones included.",
		"# TYPE kbark_incidents gauge",
		`kbark_incidents{cluster="dev",state="proposed"} 1`,
		`kbark_incidents{cluster="staging",state="resolved"} 1`,
		"# HELP kbark_incidents_open Open incidents by failure family.",
		"# TYPE kbark_incidents_open gauge",
		`kbark_incidents_open{cluster="dev",family="crash"} 1`,
		"# HELP kbark_explanations Incidents by explanation state.",
		"# TYPE kbark_explanations gauge",
		`kbark_explanations{state="pending"} 1`,
		`kbark_explanations{state="ready"} 1`,
		"# HELP kbark_process_goroutines Number of goroutines.",
		"# TYPE kbark_process_goroutines gauge",
		`kbark_process_goroutines <n>`,
		"# HELP kbark_process_heap_bytes Heap memory in use by the process.",
		"# TYPE kbark_process_heap_bytes gauge",
		`kbark_process_heap_bytes <n>`,
	}, "\n") + "\n"
	want = strings.ReplaceAll(want, "<n>", "<digits>")
	if lines := strings.Split(got, "\n"); len(lines) != strings.Count(want, "\n")+1 {
		t.Fatalf("metrics text has %d lines, want %d:\n%s", len(lines), strings.Count(want, "\n")+1, got)
	}
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	for i := range wantLines {
		// The two process lines are replaced with a number placeholder.
		g := strings.TrimSpace(gotLines[i])
		wl := wantLines[i]
		if wl == "<digits>" || strings.HasSuffix(wl, " <digits>") {
			prefix := strings.TrimSuffix(wl, " <digits>")
			if !strings.HasPrefix(g, prefix) || !isAllDigits(strings.TrimSpace(strings.TrimPrefix(g, prefix))) {
				t.Errorf("line %d = %q, want %q", i, g, wl)
			}
			continue
		}
		if g != wl {
			t.Errorf("line %d:\n got %q\nwant %q", i, g, wl)
		}
	}
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func TestMetricsLabelEscaping(t *testing.T) {
	// A label value carrying every character the format is sensitive to.
	if got := escapeLabel(`we"ird\clu` + "\nster"); got != `we\"ird\\clu\nster` {
		t.Fatalf("escapeLabel = %q", got)
	}
	// Rendered through the full writer, a newline in a value must not
	// split the sample: every line still parses as a comment or a sample.
	var b strings.Builder
	writeMetrics(&b, MetricsConfig{Version: `v"1\` + "\n2"}, nil, nil)
	for _, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "kbark_") {
			t.Errorf("line is not a comment or sample: %q", line)
		}
	}
	// The escaped label appears intact, with the quote and newline escaped.
	if !strings.Contains(b.String(), `kbark_build_info{version="v\"1\\\n2"} 1`) {
		t.Errorf("escaped version label not found in:\n%s", b.String())
	}
}

func TestMetricsHandlerServesTheExposition(t *testing.T) {
	s := newServer(t, false)
	rec := do(s.MetricsHandler(), "GET", "/metrics", "ignored", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") || !strings.Contains(ct, "version=0.0.4") {
		t.Fatalf("content type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"# TYPE kbark_build_info gauge", "kbark_process_goroutines"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}
