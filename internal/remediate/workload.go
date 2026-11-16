// SPDX-License-Identifier: Apache-2.0

package remediate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/yaml"
)

// ErrPrecondition means the target changed after the fix was proposed.
// The caller should re-plan and ask for approval again.
var ErrPrecondition = errors.New("the target changed since this fix was proposed")

type editMode int

const (
	// modePreview computes the change without contacting the API for
	// the write. Used in unit tests against the fake clientset, which
	// ignores the dry-run option.
	modePreview editMode = iota
	// modeDryRun sends the change with dryRun=All so the API server
	// validates and admits it without persisting.
	modeDryRun
	// modeApply persists the change.
	modeApply
)

// workload is the part of a Deployment, StatefulSet or DaemonSet that
// kbark edits: its identity and its pod template.
type workload struct {
	meta     *metav1.ObjectMeta
	template *corev1.PodTemplateSpec
	update   func(ctx context.Context, opts metav1.UpdateOptions) (generation int64, err error)
}

func getWorkload(ctx context.Context, client kubernetes.Interface, ns, kind, name string) (*workload, error) {
	apps := client.AppsV1()
	switch kind {
	case "Deployment":
		o, err := apps.Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &workload{&o.ObjectMeta, &o.Spec.Template, func(ctx context.Context, opts metav1.UpdateOptions) (int64, error) {
			out, err := apps.Deployments(ns).Update(ctx, o, opts)
			if err != nil {
				return 0, err
			}
			return out.Generation, nil
		}}, nil
	case "StatefulSet":
		o, err := apps.StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &workload{&o.ObjectMeta, &o.Spec.Template, func(ctx context.Context, opts metav1.UpdateOptions) (int64, error) {
			out, err := apps.StatefulSets(ns).Update(ctx, o, opts)
			if err != nil {
				return 0, err
			}
			return out.Generation, nil
		}}, nil
	case "DaemonSet":
		o, err := apps.DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &workload{&o.ObjectMeta, &o.Spec.Template, func(ctx context.Context, opts metav1.UpdateOptions) (int64, error) {
			out, err := apps.DaemonSets(ns).Update(ctx, o, opts)
			if err != nil {
				return 0, err
			}
			return out.Generation, nil
		}}, nil
	}
	return nil, fmt.Errorf("kbark does not edit %s objects", kind)
}

// editable reports whether kind has a pod template kbark can change.
func editable(kind string) bool {
	return kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet"
}

type editResult struct {
	UID           string
	Generation    int64 // before the edit
	NewGeneration int64 // after the edit (apply mode only)
	Before, After []byte
	Diff          string
	Meta          *metav1.ObjectMeta
}

// editTemplate reads a workload, checks it is the object the caller
// expects, applies mutate to its pod template and writes it back
// according to mode. wantUID and wantGeneration are the precondition;
// pass "" and 0 to skip it (first-time planning).
func editTemplate(
	ctx context.Context, client kubernetes.Interface,
	ns, kind, name, wantUID string, wantGeneration int64,
	mode editMode, mutate func(*corev1.PodTemplateSpec) error,
) (editResult, error) {
	var res editResult
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		w, err := getWorkload(ctx, client, ns, kind, name)
		if err != nil {
			return err
		}
		if wantUID != "" && (string(w.meta.UID) != wantUID || w.meta.Generation != wantGeneration) {
			return ErrPrecondition
		}
		res.UID, res.Generation, res.Meta = string(w.meta.UID), w.meta.Generation, w.meta
		if res.Before, err = json.Marshal(w.template); err != nil {
			return err
		}
		if err := mutate(w.template); err != nil {
			return err
		}
		if res.After, err = json.Marshal(w.template); err != nil {
			return err
		}
		res.Diff = templateDiff(kind, name, res.Before, res.After)
		if res.Diff == "" {
			return errors.New("the fix would not change anything")
		}
		switch mode {
		case modeDryRun:
			_, err = w.update(ctx, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
		case modeApply:
			res.NewGeneration, err = w.update(ctx, metav1.UpdateOptions{FieldManager: "kbark"})
		}
		return err
	})
	return res, err
}

// templateDiff renders the change to a pod template as a unified diff
// of its YAML, which is what the approver reads before deciding.
func templateDiff(kind, name string, before, after []byte) string {
	a, errA := yaml.JSONToYAML(before)
	b, errB := yaml.JSONToYAML(after)
	if errA != nil || errB != nil {
		return ""
	}
	label := strings.ToLower(kind) + "/" + name + " spec.template"
	out, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(string(a)), B: difflib.SplitLines(string(b)),
		FromFile: label + " (current)", ToFile: label + " (proposed)", Context: 2,
	})
	if err != nil {
		return ""
	}
	return out
}

// findContainer returns the named container from the template, looking
// at init containers when init is set.
func findContainer(t *corev1.PodTemplateSpec, name string, init bool) *corev1.Container {
	list := t.Spec.Containers
	if init {
		list = t.Spec.InitContainers
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// managedBy names the delivery tool that owns a workload, or "".
// A live patch to such a workload drifts from its source of truth.
func managedBy(m *metav1.ObjectMeta) string {
	switch {
	case m.Labels["app.kubernetes.io/managed-by"] == "Helm":
		return "Helm"
	case m.Annotations["argocd.argoproj.io/tracking-id"] != "", m.Labels["argocd.argoproj.io/instance"] != "":
		return "Argo CD"
	case m.Labels["kustomize.toolkit.fluxcd.io/name"] != "", m.Labels["helm.toolkit.fluxcd.io/name"] != "":
		return "Flux"
	}
	return ""
}
