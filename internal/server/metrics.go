// SPDX-License-Identifier: Apache-2.0

// The Prometheus text endpoint. Hand-written rather than pulled from a
// client library: kbark needs a handful of gauges over data it already
// holds in memory, and the exposition format is small enough that a
// dependency would buy nothing but a supply-chain surface.
//
// The endpoint is served by a separate listener (see --metrics-addr in
// cmd/kbark/serve.go), never on the console's port: metrics scrapers do
// not sign in, and the console must not grow an unauthenticated route.

package server

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strings"

	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
)

// MetricsConfig is the static part of the metrics output.
type MetricsConfig struct {
	// Version is the kbark build being scraped.
	Version string
}

// writeMetrics renders the whole exposition text. Every series is
// emitted exactly once, in a fixed order, so two scrapes of an
// unchanged server differ only in the process gauges. Label sets with
// nothing to count are omitted rather than emitted as zeroes: a zero
// for a state no incident ever reached carries no information.
func writeMetrics(w io.Writer, cfg MetricsConfig, snaps []fleet.Snapshot, incidents []*incident.Incident) {
	stateCounts := map[string]int{}  // "cluster|state" -> incidents in that state
	familyCounts := map[string]int{} // "cluster|family" -> open incidents
	explCounts := map[string]int{}   // explanation state -> incidents
	for _, inc := range incidents {
		stateCounts[inc.Cluster+"|"+string(inc.State)]++
		if inc.ClosedAt == nil {
			familyCounts[inc.Cluster+"|"+string(inc.Family)]++
		}
		explCounts[inc.ExplanationState]++
	}

	var b strings.Builder

	b.WriteString("# HELP kbark_build_info Build information, constant 1.\n")
	b.WriteString("# TYPE kbark_build_info gauge\n")
	fmt.Fprintf(&b, "kbark_build_info{version=\"%s\"} 1\n", escapeLabel(cfg.Version))

	b.WriteString("# HELP kbark_cluster_connected Whether the cluster's caches are warm (1) or not (0).\n")
	b.WriteString("# TYPE kbark_cluster_connected gauge\n")
	for _, s := range snaps {
		fmt.Fprintf(&b, "kbark_cluster_connected{cluster=\"%s\"} %d\n", escapeLabel(s.Summary.Name), bool01(s.Summary.State == "connected"))
	}

	b.WriteString("# HELP kbark_pods Pods by health.\n")
	b.WriteString("# TYPE kbark_pods gauge\n")
	for _, s := range snaps {
		for _, row := range []struct {
			health string
			n      int
		}{{"healthy", s.Summary.Healthy}, {"failing", s.Summary.Failing}, {"pending", s.Summary.Pending}} {
			fmt.Fprintf(&b, "kbark_pods{cluster=\"%s\",health=\"%s\"} %d\n", escapeLabel(s.Summary.Name), row.health, row.n)
		}
	}

	b.WriteString("# HELP kbark_nodes Nodes by state.\n")
	b.WriteString("# TYPE kbark_nodes gauge\n")
	for _, s := range snaps {
		for _, row := range []struct {
			state string
			n     int
		}{
			{"ready", s.Summary.NodesReady},
			{"not_ready", s.Summary.Nodes - s.Summary.NodesReady},
			{"cordoned", s.Summary.Cordoned},
		} {
			fmt.Fprintf(&b, "kbark_nodes{cluster=\"%s\",state=\"%s\"} %d\n", escapeLabel(s.Summary.Name), row.state, row.n)
		}
	}

	// Incidents per lifecycle state, closed ones included: what the
	// store holds now, not only what is open.
	b.WriteString("# HELP kbark_incidents Incidents by lifecycle state, closed ones included.\n")
	b.WriteString("# TYPE kbark_incidents gauge\n")
	for _, k := range sortedKeys(stateCounts) {
		cluster, state, _ := strings.Cut(k, "|")
		fmt.Fprintf(&b, "kbark_incidents{cluster=\"%s\",state=\"%s\"} %d\n", escapeLabel(cluster), escapeLabel(state), stateCounts[k])
	}

	// Open incidents split by failure family: how much is failing, and
	// what kind.
	b.WriteString("# HELP kbark_incidents_open Open incidents by failure family.\n")
	b.WriteString("# TYPE kbark_incidents_open gauge\n")
	for _, k := range sortedKeys(familyCounts) {
		cluster, family, _ := strings.Cut(k, "|")
		fmt.Fprintf(&b, "kbark_incidents_open{cluster=\"%s\",family=\"%s\"} %d\n", escapeLabel(cluster), escapeLabel(family), familyCounts[k])
	}

	b.WriteString("# HELP kbark_explanations Incidents by explanation state.\n")
	b.WriteString("# TYPE kbark_explanations gauge\n")
	for _, state := range sortedKeys(explCounts) {
		fmt.Fprintf(&b, "kbark_explanations{state=\"%s\"} %d\n", escapeLabel(state), explCounts[state])
	}

	b.WriteString("# HELP kbark_process_goroutines Number of goroutines.\n")
	b.WriteString("# TYPE kbark_process_goroutines gauge\n")
	fmt.Fprintf(&b, "kbark_process_goroutines %d\n", runtime.NumGoroutine())

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	b.WriteString("# HELP kbark_process_heap_bytes Heap memory in use by the process.\n")
	b.WriteString("# TYPE kbark_process_heap_bytes gauge\n")
	fmt.Fprintf(&b, "kbark_process_heap_bytes %d\n", mem.HeapAlloc)

	_, _ = w.Write([]byte(b.String()))
}

func bool01(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// escapeLabel makes a value safe inside a label. The exposition format
// defines escapes for backslash and double quote; a newline would break
// the line-oriented format entirely, so it is escaped too, which
// scrapers accept.
func escapeLabel(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(v)
}

// MetricsHandler serves the exposition text. Mount it on the metrics
// listener only (see cmd/kbark/serve.go); the console keeps requiring a
// session for everything under /api.
func (s *Server) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writeMetrics(w, MetricsConfig{Version: s.Info.Version}, s.Engine.Snapshots(), s.Store.List())
	})
}
