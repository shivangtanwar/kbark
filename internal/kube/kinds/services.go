// SPDX-License-Identifier: Apache-2.0

package kinds

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/shivangtanwar/kbark/internal/tui/components"
)

// Services is the plugin for core/v1 Services. Columns mirror
// `kubectl get services`.
func Services() Plugin {
	return Plugin{
		Key:         "svc",
		DisplayName: "Services",
		GVR:         corev1.SchemeGroupVersion.WithResource("services"),
		Columns: []table.Column{
			{Title: "NAME", Width: 30},
			{Title: "TYPE", Width: 14},
			{Title: "CLUSTER-IP", Width: 15},
			{Title: "EXTERNAL-IP", Width: 15},
			{Title: "PORTS", Width: 20},
			{Title: "AGE", Width: 8},
		},
		Row: func(obj runtime.Object) table.Row {
			s, ok := obj.(*corev1.Service)
			if !ok {
				return table.Row{"<malformed>", "", "", "", "", ""}
			}
			return table.Row{
				s.Name,
				string(s.Spec.Type),
				clusterIP(s),
				externalIP(s),
				servicePortList(s),
				components.FormatAge(s.CreationTimestamp.Time),
			}
		},
	}
}

func clusterIP(s *corev1.Service) string {
	if s.Spec.ClusterIP == "" {
		return "<none>"
	}
	return s.Spec.ClusterIP
}

// externalIP picks one external endpoint to display, preferring
// LoadBalancer ingress (hostname or IP), falling back to the
// configured ExternalIPs slice, then "<none>". Matches the
// single-cell summary kubectl shows under EXTERNAL-IP.
func externalIP(s *corev1.Service) string {
	for _, ing := range s.Status.LoadBalancer.Ingress {
		if ing.Hostname != "" {
			return ing.Hostname
		}
		if ing.IP != "" {
			return ing.IP
		}
	}
	if len(s.Spec.ExternalIPs) > 0 {
		return strings.Join(s.Spec.ExternalIPs, ",")
	}
	if s.Spec.Type == corev1.ServiceTypeLoadBalancer {
		return "<pending>"
	}
	return "<none>"
}

func servicePortList(s *corev1.Service) string {
	if len(s.Spec.Ports) == 0 {
		return "<none>"
	}
	parts := make([]string, 0, len(s.Spec.Ports))
	for _, p := range s.Spec.Ports {
		entry := strconv.Itoa(int(p.Port))
		if p.NodePort != 0 {
			entry += ":" + strconv.Itoa(int(p.NodePort))
		}
		entry += "/" + string(p.Protocol)
		parts = append(parts, entry)
	}
	return strings.Join(parts, ",")
}
