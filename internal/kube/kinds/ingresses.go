// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strings"

	"github.com/charmbracelet/bubbles/table"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// Ingresses is the plugin for networking.k8s.io/v1 Ingresses. Columns
// mirror `kubectl get ingress`: name, class, hosts, address, age.
// PORTS is omitted — the kubectl form is "80, 443" and rarely
// informative compared to seeing the actual hosts.
func Ingresses() Plugin {
	return Plugin{
		Key:         "ing",
		DisplayName: "Ingresses",
		GVR:         networkingv1.SchemeGroupVersion.WithResource("ingresses"),
		Columns: []table.Column{
			{Title: "NAME", Width: 30},
			{Title: "CLASS", Width: 14},
			{Title: "HOSTS", Width: 30},
			{Title: "ADDRESS", Width: 24},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			ing, ok := obj.(*networkingv1.Ingress)
			if !ok {
				return table.Row{"<malformed>", "", "", "", ""}
			}
			return table.Row{
				ing.Name,
				ingressClass(ing),
				ingressHosts(ing),
				ingressAddress(ing),
				components.FormatAge(ing.CreationTimestamp.Time),
			}
		},
	}
}

func ingressClass(ing *networkingv1.Ingress) string {
	if ing.Spec.IngressClassName != nil && *ing.Spec.IngressClassName != "" {
		return *ing.Spec.IngressClassName
	}
	// Fall back to the legacy annotation kubectl also checks.
	if v, ok := ing.Annotations["kubernetes.io/ingress.class"]; ok && v != "" {
		return v
	}
	return "<none>"
}

// ingressHosts joins all distinct rule hosts. Empty host (catch-all)
// surfaces as "*" — matches kubectl's display.
func ingressHosts(ing *networkingv1.Ingress) string {
	if len(ing.Spec.Rules) == 0 {
		return "<none>"
	}
	seen := make(map[string]struct{}, len(ing.Spec.Rules))
	out := make([]string, 0, len(ing.Spec.Rules))
	for _, r := range ing.Spec.Rules {
		host := r.Host
		if host == "" {
			host = "*"
		}
		if _, dup := seen[host]; dup {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	return strings.Join(out, ",")
}

func ingressAddress(ing *networkingv1.Ingress) string {
	addrs := make([]string, 0, len(ing.Status.LoadBalancer.Ingress))
	for _, lb := range ing.Status.LoadBalancer.Ingress {
		if lb.Hostname != "" {
			addrs = append(addrs, lb.Hostname)
			continue
		}
		if lb.IP != "" {
			addrs = append(addrs, lb.IP)
		}
	}
	if len(addrs) == 0 {
		return ""
	}
	return strings.Join(addrs, ",")
}
