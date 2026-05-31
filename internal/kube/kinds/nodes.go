// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// Nodes is the plugin for core/v1 Nodes. Cluster-scoped, so the
// ResourceService ignores `:ns` when this kind is active. Columns
// mirror `kubectl get nodes`: name, ready state, roles, age, kubelet
// version. Internal/external IPs and OS info are omitted — too wide
// for the table and rarely the key diagnostic info.
func Nodes() Plugin {
	return Plugin{
		Key:         "no",
		DisplayName: "Nodes",
		Kind:        "Node",
		GVR:         corev1.SchemeGroupVersion.WithResource("nodes"),
		Scope:       Cluster,
		Columns: []table.Column{
			{Title: "NAME", Width: 40},
			{Title: "STATUS", Width: 12},
			{Title: "ROLES", Width: 24},
			{Title: "AGE", Width: 8},
			{Title: "VERSION", Width: 14},
		},
		Row: func(obj runtime.Object) table.Row {
			n, ok := obj.(*corev1.Node)
			if !ok {
				return table.Row{"<malformed>", "", "", "", ""}
			}
			return table.Row{
				n.Name,
				nodeReady(n),
				nodeRoles(n),
				components.FormatAge(n.CreationTimestamp.Time),
				n.Status.NodeInfo.KubeletVersion,
			}
		},
	}
}

// nodeReady scans the Ready condition. "Unknown" surfaces a kubelet
// that hasn't reported recently; "NotReady" is the unhealthy case.
func nodeReady(n *corev1.Node) string {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			switch c.Status {
			case corev1.ConditionTrue:
				return "Ready"
			case corev1.ConditionFalse:
				return "NotReady"
			default:
				return "Unknown"
			}
		}
	}
	return "Unknown"
}

// nodeRoles extracts role names from the `node-role.kubernetes.io/<role>`
// label convention. Returns "<none>" for worker-only nodes with no role
// label. Roles are sorted for stable display.
func nodeRoles(n *corev1.Node) string {
	const prefix = "node-role.kubernetes.io/"
	var roles []string
	for k := range n.Labels {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		role := strings.TrimPrefix(k, prefix)
		if role != "" {
			roles = append(roles, role)
		}
	}
	if len(roles) == 0 {
		return "<none>"
	}
	sort.Strings(roles)
	return strings.Join(roles, ",")
}
