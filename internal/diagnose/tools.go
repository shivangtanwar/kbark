// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/shivangtanwar/kbark/internal/ai"
)

// ToolNames are the canonical identifiers the model uses to call into
// kbark's read-only Kubernetes surface. Exported so tests + the UI layer
// can refer to them without string typos.
const (
	ToolGetEvents       = "get_events"
	ToolGetLogs         = "get_logs"
	ToolGetPreviousLogs = "get_previous_logs"
	ToolDescribePod     = "describe_pod"
	ToolGetResource     = "get_resource"
)

// ToolLogTailLines is the cap a model may request for log tailing via the
// tool layer. Independent of LogTailLines so we can tune them separately.
const ToolLogTailLines int64 = 500

// Dispatcher routes a ToolCallEvent to the right K8s API call and formats
// the result as text for the model. All operations are read-only and
// scoped to the verbs in kube.AllowedVerbs.
type Dispatcher struct {
	client    kubernetes.Interface
	dynClient dynamic.Interface
}

// NewDispatcher builds a dispatcher against an existing clientset. The
// dynamic client is optional; when nil, get_resource will return an
// error rather than crash.
func NewDispatcher(client kubernetes.Interface, dyn dynamic.Interface) *Dispatcher {
	return &Dispatcher{client: client, dynClient: dyn}
}

// Tools returns the slice of tool schemas to advertise on ai.Request.Tools.
// The order is stable so providers that surface tools in their prompts
// (Anthropic surfaces this verbatim) get a consistent ordering.
func (d *Dispatcher) Tools() []ai.Tool {
	return []ai.Tool{
		{
			Name:        ToolGetEvents,
			Description: "Fetch recent events for a pod. Use when the pod's failure cause isn't obvious from the initial context — events surface scheduling, image-pull, and probe failures.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace": map[string]any{"type": "string", "description": "Namespace of the pod."},
					"name":      map[string]any{"type": "string", "description": "Pod name."},
				},
				"required": []string{"namespace", "name"},
			},
		},
		{
			Name:        ToolGetLogs,
			Description: "Fetch the most recent log lines from a pod's container. Use when you need to read the actual program output (panic messages, error logs). Defaults to all containers in the pod when container is empty.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace":  map[string]any{"type": "string", "description": "Namespace of the pod."},
					"name":       map[string]any{"type": "string", "description": "Pod name."},
					"container":  map[string]any{"type": "string", "description": "Container name within the pod. Omit for the default container."},
					"tail_lines": map[string]any{"type": "integer", "description": "Number of trailing lines to return (default 200, max 500)."},
				},
				"required": []string{"namespace", "name"},
			},
		},
		{
			Name:        ToolGetPreviousLogs,
			Description: "Fetch the logs from the previous instance of a container (i.e., before its last restart). Use this on CrashLoopBackOff pods to see what the container printed before it crashed.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace":  map[string]any{"type": "string", "description": "Namespace of the pod."},
					"name":       map[string]any{"type": "string", "description": "Pod name."},
					"container":  map[string]any{"type": "string", "description": "Container name within the pod."},
					"tail_lines": map[string]any{"type": "integer", "description": "Number of trailing lines to return (default 200, max 500)."},
				},
				"required": []string{"namespace", "name"},
			},
		},
		{
			Name:        ToolDescribePod,
			Description: "Get the full descriptive metadata for a pod: spec, status, containers, conditions, volumes. Use when the initial context's container summary doesn't expose enough about the pod's spec (mounts, probes, resource limits).",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace": map[string]any{"type": "string", "description": "Namespace of the pod."},
					"name":      map[string]any{"type": "string", "description": "Pod name."},
				},
				"required": []string{"namespace", "name"},
			},
		},
		{
			Name:        ToolGetResource,
			Description: "Generic read of a related resource (ConfigMap, Secret, Service, Deployment, ReplicaSet, etc.). Use to inspect resources the pod references — e.g. a missing ConfigMap that the pod mounts. Returns YAML.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"namespace": map[string]any{"type": "string", "description": "Namespace of the resource."},
					"kind":      map[string]any{"type": "string", "description": "Resource kind (e.g. ConfigMap, Service, Deployment). Singular, capitalized."},
					"name":      map[string]any{"type": "string", "description": "Resource name."},
				},
				"required": []string{"namespace", "kind", "name"},
			},
		},
	}
}

// Dispatch executes a single tool call and returns the formatted text
// result. Returns a typed error only on truly unexpected failures
// (unknown tool, malformed JSON). API-level failures get rendered into
// the result string so the model can interpret them ("not found",
// "forbidden", …) instead of treating them as crash-worthy.
func (d *Dispatcher) Dispatch(ctx context.Context, call ai.ToolCallEvent) (string, error) {
	switch call.Name {
	case ToolGetEvents:
		return d.getEvents(ctx, call.Arguments)
	case ToolGetLogs:
		return d.getLogs(ctx, call.Arguments, false)
	case ToolGetPreviousLogs:
		return d.getLogs(ctx, call.Arguments, true)
	case ToolDescribePod:
		return d.describePod(ctx, call.Arguments)
	case ToolGetResource:
		return d.getResource(ctx, call.Arguments)
	default:
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}
}

// ---- per-tool input types and implementations ----

type podRefInput struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type logsInput struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Container string `json:"container"`
	TailLines int64  `json:"tail_lines"`
}

type resourceInput struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

func (d *Dispatcher) getEvents(ctx context.Context, raw string) (string, error) {
	var in podRefInput
	if err := unmarshalArgs(raw, &in); err != nil {
		return "", err
	}
	if in.Namespace == "" || in.Name == "" {
		return "namespace and name are required", nil
	}
	selector := fmt.Sprintf("involvedObject.name=%s,involvedObject.namespace=%s", in.Name, in.Namespace)
	list, err := d.client.CoreV1().Events(in.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: selector,
		Limit:         EventListLimit,
	})
	if err != nil {
		return fmt.Sprintf("(events lookup failed: %v)", err), nil
	}
	events := list.Items
	sort.Slice(events, func(i, j int) bool {
		return mostRecent(events[i]).After(mostRecent(events[j]))
	})
	if len(events) > EventsInPayload {
		events = events[:EventsInPayload]
	}
	if len(events) == 0 {
		return fmt.Sprintf("no events for %s/%s", in.Namespace, in.Name), nil
	}
	var b strings.Builder
	for _, e := range events {
		when := mostRecent(e)
		fmt.Fprintf(&b, "[%s] %s %s: %s\n",
			when.Format(time.RFC3339), e.Type, e.Reason, strings.TrimSpace(e.Message))
	}
	return b.String(), nil
}

func (d *Dispatcher) getLogs(ctx context.Context, raw string, previous bool) (string, error) {
	var in logsInput
	if err := unmarshalArgs(raw, &in); err != nil {
		return "", err
	}
	if in.Namespace == "" || in.Name == "" {
		return "namespace and name are required", nil
	}
	tail := in.TailLines
	if tail <= 0 {
		tail = LogTailLines
	}
	if tail > ToolLogTailLines {
		tail = ToolLogTailLines
	}

	timedCtx, cancel := context.WithTimeout(ctx, LogReadTimeout)
	defer cancel()

	opts := &corev1.PodLogOptions{
		Container: in.Container,
		Previous:  previous,
		TailLines: &tail,
	}
	req := d.client.CoreV1().Pods(in.Namespace).GetLogs(in.Name, opts)
	stream, err := req.Stream(timedCtx)
	if err != nil {
		return fmt.Sprintf("(log read failed: %v)", err), nil
	}
	defer stream.Close()
	data, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Sprintf("(log read failed: %v)", err), nil
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return fmt.Sprintf("(no logs for %s/%s container=%s previous=%v)", in.Namespace, in.Name, in.Container, previous), nil
	}
	return text, nil
}

func (d *Dispatcher) describePod(ctx context.Context, raw string) (string, error) {
	var in podRefInput
	if err := unmarshalArgs(raw, &in); err != nil {
		return "", err
	}
	if in.Namespace == "" || in.Name == "" {
		return "namespace and name are required", nil
	}
	pod, err := d.client.CoreV1().Pods(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Sprintf("(pod lookup failed: %v)", err), nil
	}
	var b strings.Builder
	writeStatus(&b, pod)
	writeContainers(&b, pod)
	writeSpecExtras(&b, pod)
	return strings.TrimRight(b.String(), "\n"), nil
}

func (d *Dispatcher) getResource(ctx context.Context, raw string) (string, error) {
	var in resourceInput
	if err := unmarshalArgs(raw, &in); err != nil {
		return "", err
	}
	if d.dynClient == nil {
		return "(dynamic client not available; ask for a more specific tool)", nil
	}
	if in.Namespace == "" || in.Kind == "" || in.Name == "" {
		return "namespace, kind, and name are required", nil
	}
	gvr, ok := gvrForKind(in.Kind)
	if !ok {
		return fmt.Sprintf("(unknown kind %q; supported: ConfigMap, Secret, Service, Endpoints, Deployment, ReplicaSet, StatefulSet, DaemonSet, Job, CronJob, Ingress, PersistentVolumeClaim)", in.Kind), nil
	}
	obj, err := d.dynClient.Resource(gvr).Namespace(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Sprintf("(get %s/%s/%s failed: %v)", in.Kind, in.Namespace, in.Name, err), nil
	}
	// Redact obvious secrets so we never leak them to the AI. v1 spec
	// has a full redaction pass in M8; this is the minimum here so
	// Secret.data doesn't go on the wire.
	if in.Kind == "Secret" {
		unstructured := obj.UnstructuredContent()
		if d, ok := unstructured["data"].(map[string]any); ok {
			for k := range d {
				d[k] = "<redacted>"
			}
		}
	}
	out, err := json.MarshalIndent(obj.UnstructuredContent(), "", "  ")
	if err != nil {
		return fmt.Sprintf("(marshal failed: %v)", err), nil
	}
	return string(out), nil
}

// writeSpecExtras adds the spec details not covered by writeContainers
// (volumes, volume mounts via container references). Keeps the output
// terse — the model only needs the spec when it's investigating a
// specific failure mode.
func writeSpecExtras(out *strings.Builder, pod *corev1.Pod) {
	if len(pod.Spec.Volumes) == 0 {
		return
	}
	fmt.Fprintln(out, "### Volumes")
	for _, v := range pod.Spec.Volumes {
		fmt.Fprintf(out, "- %s:", v.Name)
		switch {
		case v.ConfigMap != nil:
			fmt.Fprintf(out, " configMap=%s", v.ConfigMap.Name)
		case v.Secret != nil:
			fmt.Fprintf(out, " secret=%s", v.Secret.SecretName)
		case v.PersistentVolumeClaim != nil:
			fmt.Fprintf(out, " pvc=%s", v.PersistentVolumeClaim.ClaimName)
		case v.EmptyDir != nil:
			fmt.Fprint(out, " emptyDir")
		case v.HostPath != nil:
			fmt.Fprintf(out, " hostPath=%s", v.HostPath.Path)
		default:
			fmt.Fprint(out, " (other)")
		}
		fmt.Fprintln(out)
	}
}

func unmarshalArgs(raw string, into any) error {
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), into); err != nil {
		return fmt.Errorf("malformed tool arguments: %w", err)
	}
	return nil
}

// gvrForKind maps the human-friendly kind name to a GroupVersionResource
// the dynamic client can serve. Limited to common namespaced resources
// kbark cares about — extending the map is cheap and safe.
func gvrForKind(kind string) (schema.GroupVersionResource, bool) {
	switch kind {
	case "ConfigMap":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}, true
	case "Secret":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}, true
	case "Service":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, true
	case "Endpoints":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "endpoints"}, true
	case "PersistentVolumeClaim":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}, true
	case "Pod":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, true
	case "Deployment":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, true
	case "ReplicaSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, true
	case "StatefulSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, true
	case "DaemonSet":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, true
	case "Job":
		return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, true
	case "CronJob":
		return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, true
	case "Ingress":
		return schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, true
	}
	return schema.GroupVersionResource{}, false
}
