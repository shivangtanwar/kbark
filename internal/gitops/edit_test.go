// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// deployment is a two-document file: a Deployment with a web and an
// init container, and an unrelated Service, all with comments worth
// keeping.
const deployment = `# Managed by platform team; changes go through review.
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  replicas: 3
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
    spec:
      # sidecars come after the main container
      initContainers:
      - name: warm
        image: busybox
      containers:
      - name: web
        image: nginx:1.27
        resources:
          limits:
            memory: 64Mi
          requests:
            memory: 64Mi
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: web
  labels: {app: web}
spec:
  selector:
    app: web
  ports:
  - port: 80
    targetPort: 8080
`

// edit is the shorthand every test case uses.
func edit(t *testing.T, file string, c Change) (string, error) {
	t.Helper()
	out, err := Edit([]byte(file), c)
	return string(out), err
}

func TestEditActions(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		change  Change
		want    string // substring of the result
		notWant string // must not appear
		wantErr error
	}{
		{
			name: "memory limit raised, request follows",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "128Mi"},
			},
			want: "resources:\n            limits:\n              memory: 128Mi\n            requests:\n              memory: 128Mi",
		},
		{
			name: "memory limit raised, request kept when it differed",
			file: strings.Replace(deployment, "requests:\n            memory: 64Mi", "requests:\n            memory: 32Mi", 1),
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "128Mi"},
			},
			want:    "resources:\n            limits:\n              memory: 128Mi\n            requests:\n              memory: 32Mi",
			notWant: "requests:\n              memory: 128Mi",
		},
		{
			name: "memory limit created when absent",
			file: strings.Replace(deployment, "        resources:\n          limits:\n            memory: 64Mi\n          requests:\n            memory: 64Mi\n", "", 1),
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "128Mi"},
			},
			want: "resources:\n            limits:\n              memory: 128Mi",
		},
		{
			name: "memory limit on init container",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "warm", "init": "true", "to": "256Mi"},
			},
			want: "warm\n          image: busybox\n          resources:\n            limits:\n              memory: 256Mi",
		},
		{
			name: "memory no change",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "64Mi"},
			},
			wantErr: ErrNoChange,
		},
		{
			name: "memory quantity semantics: 0.5Gi is 512Mi",
			file: strings.Replace(deployment, "memory: 64Mi\n          requests:\n            memory: 64Mi", "memory: 0.5Gi\n          requests:\n            memory: 512Mi", 1),
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "0.5Gi"},
			},
			wantErr: ErrNoChange,
		},
		{
			name: "request follows old limit across spellings",
			file: strings.Replace(deployment, "memory: 64Mi\n          requests:\n            memory: 64Mi", "memory: 0.5Gi\n          requests:\n            memory: 512Mi", 1),
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "1Gi"},
			},
			want: "limits:\n              memory: 1Gi\n            requests:\n              memory: 1Gi",
		},
		{
			name: "ephemeral storage limit raised",
			file: strings.Replace(deployment, "limits:\n            memory: 64Mi", "limits:\n            memory: 64Mi\n            ephemeral-storage: 1Gi", 1),
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetEphemeralLimit,
				Params: map[string]string{"container": "web", "to": "2Gi"},
			},
			want: "ephemeral-storage: 2Gi",
		},
		{
			name: "ephemeral storage limit created when absent",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetEphemeralLimit,
				Params: map[string]string{"container": "web", "to": "2Gi"},
			},
			want: "limits:\n              memory: 64Mi\n              ephemeral-storage: 2Gi",
		},
		{
			name: "probe tuned on existing keys",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeTuneProbe,
				Params: map[string]string{"container": "web", "probe": "liveness", "initialDelaySeconds": "30", "timeoutSeconds": "5"},
			},
			want: "initialDelaySeconds: 30\n            timeoutSeconds: 5",
		},
		{
			name: "probe keys created when absent",
			file: strings.Replace(deployment, "livenessProbe:", "readinessProbe:\n          httpGet:\n            path: /ready\n            port: 8080\n        livenessProbe:", 1),
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeTuneProbe,
				Params: map[string]string{"container": "web", "probe": "readiness", "initialDelaySeconds": "10", "timeoutSeconds": "3"},
			},
			want: "readinessProbe:\n            httpGet:\n              path: /ready\n              port: 8080\n            initialDelaySeconds: 10\n            timeoutSeconds: 3",
		},
		{
			name: "startup probe tuned",
			file: strings.Replace(deployment, "livenessProbe:", "startupProbe:\n          httpGet:\n            path: /started\n            port: 8080\n        livenessProbe:", 1),
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeTuneProbe,
				Params: map[string]string{"container": "web", "probe": "startup", "initialDelaySeconds": "15", "timeoutSeconds": "4"},
			},
			want: "startupProbe:\n            httpGet:\n              path: /started\n              port: 8080\n            initialDelaySeconds: 15\n            timeoutSeconds: 4",
		},
		{
			name: "probe no change",
			file: strings.Replace(deployment, "initialDelaySeconds: 5", "initialDelaySeconds: 5\n          timeoutSeconds: 3", 1),
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeTuneProbe,
				Params: map[string]string{"container": "web", "probe": "liveness", "initialDelaySeconds": "5", "timeoutSeconds": "3"},
			},
			wantErr: ErrNoChange,
		},
		{
			name: "probe missing",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeTuneProbe,
				Params: map[string]string{"container": "web", "probe": "readiness", "initialDelaySeconds": "10", "timeoutSeconds": "3"},
			},
			wantErr: ErrObjectNotFound,
		},
		{
			name: "pull policy set",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetPullPolicy,
				Params: map[string]string{"container": "web", "to": "IfNotPresent"},
			},
			want: "imagePullPolicy: IfNotPresent",
		},
		{
			name: "pull policy set on init container",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetPullPolicy,
				Params: map[string]string{"container": "warm", "init": "true", "to": "IfNotPresent"},
			},
			want: "warm\n          image: busybox\n          imagePullPolicy: IfNotPresent",
		},
		{
			name: "pull policy no change",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetPullPolicy,
				Params: map[string]string{"container": "web", "to": "IfNotPresent"},
			},
			want: "imagePullPolicy: IfNotPresent",
		},
		{
			name: "service selector fixed",
			file: deployment,
			change: Change{
				Kind: "Service", Name: "web", Type: typeFixSelector,
				Params: map[string]string{"from": `{"app":"web"}`, "to": `{"app":"Web"}`},
			},
			want: "selector:\n    app: Web",
		},
		{
			name: "service selector already fixed",
			file: strings.Replace(deployment, "  selector:\n    app: web\n  ports:", "  selector:\n    app: Web\n  ports:", 1),
			change: Change{
				Kind: "Service", Name: "web", Type: typeFixSelector,
				Params: map[string]string{"from": `{"app":"web"}`, "to": `{"app":"Web"}`},
			},
			wantErr: ErrNoChange,
		},
		{
			name: "namespace must match when present",
			file: strings.Replace(deployment, "metadata:\n  name: web\nspec:\n  replicas", "metadata:\n  name: web\n  namespace: prod\nspec:\n  replicas", 1),
			change: Change{
				Kind: "Deployment", Namespace: "dev", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "128Mi"},
			},
			wantErr: ErrObjectNotFound,
		},
		{
			name: "namespace-less manifest matches any namespace",
			file: deployment,
			change: Change{
				Kind: "Deployment", Namespace: "prod", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "128Mi"},
			},
			want: "memory: 128Mi",
		},
		{
			name: "object not found",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "other", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "128Mi"},
			},
			wantErr: ErrObjectNotFound,
		},
		{
			name: "container not found",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "sidecar", "to": "128Mi"},
			},
			wantErr: ErrObjectNotFound,
		},
		{
			name: "rollback unsupported",
			file: deployment,
			change: Change{
				Kind: "Deployment", Name: "web", Type: "rollback-workload",
				Params: map[string]string{"fromRevision": "3", "toRevision": "2"},
			},
			wantErr: ErrUnsupported,
		},
		{
			name: "delete-pod unsupported",
			file: deployment,
			change: Change{
				Kind: "Pod", Name: "web-1", Type: "delete-pod",
				Params: map[string]string{"pods": "web-1=abc"},
			},
			wantErr: ErrUnsupported,
		},
		{
			name:    "restart-pod unsupported",
			file:    deployment,
			change:  Change{Kind: "Pod", Name: "web-1", Type: "restart-pod"},
			wantErr: ErrUnsupported,
		},
		{
			name:    "uncordon-node unsupported",
			file:    deployment,
			change:  Change{Kind: "Node", Name: "n1", Type: "uncordon-node"},
			wantErr: ErrUnsupported,
		},
		{
			name:    "unknown action unsupported",
			file:    deployment,
			change:  Change{Kind: "Deployment", Name: "web", Type: "bump-replicas"},
			wantErr: ErrUnsupported,
		},
		{
			name: "templated file rejected",
			file: strings.Replace(deployment, "name: web", "name: {{ .Release.Name }}-web", 1),
			change: Change{
				Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
				Params: map[string]string{"container": "web", "to": "128Mi"},
			},
			wantErr: ErrTemplated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := edit(t, tt.file, tt.change)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Edit error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Edit: %v\noutput:\n%s", err, out)
			}
			if tt.want != "" && !strings.Contains(out, tt.want) {
				t.Errorf("output missing %q:\n%s", tt.want, out)
			}
			if tt.notWant != "" && strings.Contains(out, tt.notWant) {
				t.Errorf("output unexpectedly contains %q:\n%s", tt.notWant, out)
			}
			// Every successful edit must still parse.
			if _, err := decodeDocs([]byte(out)); err != nil {
				t.Errorf("edited file does not parse: %v", err)
			}
		})
	}

	// Cases where a sentinel is wrong but a specific message is right.
	mismatched := strings.Replace(deployment, "  selector:\n    app: web\n  ports:", "  selector:\n    app: other\n  ports:", 1)
	out, err := edit(t, mismatched, Change{
		Kind: "Service", Name: "web", Type: typeFixSelector,
		Params: map[string]string{"from": `{"app":"web"}`, "to": `{"app":"Web"}`},
	})
	if err == nil || !strings.Contains(err.Error(), "planned from") {
		t.Errorf("selector mismatch error = %v, want one naming the stale selector (out:\n%s)", err, out)
	}
	out, err = edit(t, deployment, Change{
		Kind: "Deployment", Name: "web", Type: typeSetPullPolicy,
		Params: map[string]string{"container": "web", "to": "Always"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported pull policy") {
		t.Errorf("pull policy error = %v, want unsupported pull policy (out:\n%s)", err, out)
	}
	out, err = edit(t, deployment, Change{
		Kind: "Deployment", Name: "web", Type: typeTuneProbe,
		Params: map[string]string{"container": "web", "probe": "liveness", "initialDelaySeconds": "x", "timeoutSeconds": "3"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid probe timing") {
		t.Errorf("probe timing error = %v, want invalid probe timing (out:\n%s)", err, out)
	}
}

func TestEditPreservesOtherDocuments(t *testing.T) {
	out, err := edit(t, deployment, Change{
		Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
		Params: map[string]string{"container": "web", "to": "128Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The Service document, including its flow-style labels, its
	// comment and its compact sequence, is copied through unchanged.
	svc := deployment[strings.Index(deployment, "---\napiVersion: v1"):]
	if !strings.HasSuffix(out, svc) {
		t.Errorf("untouched document not byte-identical:\nwant suffix:\n%s\ngot:\n%s", svc, out)
	}
	// The head comment on the first document survives.
	if !strings.HasPrefix(out, "# Managed by platform team; changes go through review.\n") {
		t.Errorf("head comment lost:\n%s", out)
	}
	// A comment inside the edited document survives.
	if !strings.Contains(out, "# sidecars come after the main container") {
		t.Errorf("inner comment lost:\n%s", out)
	}
}

func TestEditMiddleDocument(t *testing.T) {
	file := "kind: ConfigMap\nmetadata:\n  name: first\n---\n" + deployment[strings.Index(deployment, "apiVersion"):]
	out, err := edit(t, file, Change{
		Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
		Params: map[string]string{"container": "web", "to": "128Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "kind: ConfigMap\nmetadata:\n  name: first\n") {
		t.Errorf("first document changed:\n%s", out)
	}
	if !strings.Contains(out, "memory: 128Mi") {
		t.Errorf("middle document not edited:\n%s", out)
	}
}

func TestEditNamespace4Indent(t *testing.T) {
	file := `apiVersion: apps/v1
kind: StatefulSet
metadata:
    name: db
    namespace: prod
spec:
    template:
        spec:
            containers:
                - name: db
                  image: postgres:16
                  resources:
                    limits:
                        memory: 64Mi
`
	out, err := edit(t, file, Change{
		Kind: "StatefulSet", Namespace: "prod", Name: "db", Type: typeSetMemoryLimit,
		Params: map[string]string{"container": "db", "to": "128Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "                        memory: 128Mi\n") {
		t.Errorf("4-space indent not kept:\n%s", out)
	}
}

func TestEditSequenceIndentDocumented(t *testing.T) {
	// yaml.v3 always indents sequence items under their key. The
	// input writes the dash at the key's column; the output moves it
	// in. This is the one visible reformatting of the edited document
	// and it is stable from then on.
	file := `kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      containers:
      - name: web
        image: nginx
`
	out, err := edit(t, file, Change{
		Kind: "Deployment", Name: "web", Type: typeSetPullPolicy,
		Params: map[string]string{"container": "web", "to": "IfNotPresent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "      containers:\n        - name: web\n          image: nginx\n          imagePullPolicy: IfNotPresent\n") {
		t.Errorf("sequence indentation changed unexpectedly:\n%s", out)
	}
}

func TestEditIdempotent(t *testing.T) {
	// Applying the same change twice is a no-op the second time...
	out, err := edit(t, deployment, Change{
		Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
		Params: map[string]string{"container": "web", "to": "128Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Edit([]byte(out), Change{
		Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
		Params: map[string]string{"container": "web", "to": "128Mi"},
	}); !errors.Is(err, ErrNoChange) {
		t.Fatalf("second edit error = %v, want ErrNoChange", err)
	}
	// ...and the splice is a fixpoint: re-encoding the edited
	// document of the output, with nothing further changed,
	// reproduces the output byte-for-byte, so a later edit of the
	// same document does not churn lines the first edit normalized.
	if !stable([]byte(out), Change{Kind: "Deployment", Name: "web"}) {
		t.Errorf("output is not formatting-stable:\n%s", out)
	}
	out2, err := edit(t, out, Change{
		Kind: "Deployment", Name: "web", Type: typeTuneProbe,
		Params: map[string]string{"container": "web", "probe": "liveness", "initialDelaySeconds": "30", "timeoutSeconds": "5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stable([]byte(out2), Change{Kind: "Deployment", Name: "web"}) {
		t.Errorf("second output is not formatting-stable:\n%s", out2)
	}
}

// stable reports whether re-encoding the file's matching document,
// with no further change, reproduces the file byte-for-byte.
func stable(b []byte, c Change) bool {
	docs, err := decodeDocs(b)
	if err != nil {
		return false
	}
	idx := findDoc(docs, c)
	if idx < 0 {
		return false
	}
	out, err := encodeDocs(b, docs, idx)
	return err == nil && bytes.Equal(out, b)
}

func TestEditLeadingSeparator(t *testing.T) {
	file := "---\n" + deployment
	out, err := edit(t, file, Change{
		Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
		Params: map[string]string{"container": "web", "to": "128Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "---\n") {
		t.Errorf("leading separator lost:\n%s", out)
	}
}

func TestEditCommentBetweenDocs(t *testing.T) {
	file := deployment + "---\n# a third object\nkind: ConfigMap\nmetadata:\n  name: extra\n"
	out, err := edit(t, file, Change{
		Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
		Params: map[string]string{"container": "web", "to": "128Mi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# a third object\nkind: ConfigMap") {
		t.Errorf("comment-only region lost:\n%s", out)
	}
}

func TestDetectIndent(t *testing.T) {
	tests := []struct {
		file string
		want int
	}{
		{"a: 1\n", 2},
		{"a:\n  b: 1\n", 2},
		{"a:\n    b: 1\n", 4},
		{"a: {b: 1}\n", 2},
		// One author-sloppy mapping does not collapse the file's indent.
		{"a:\n    b:\n      c: 1\n    d:\n        e: 2\n", 4},
	}
	for _, tt := range tests {
		docs, err := decodeDocs([]byte(tt.file))
		if err != nil {
			t.Fatalf("%q: %v", tt.file, err)
		}
		if got := detectIndent(docs); got != tt.want {
			t.Errorf("detectIndent(%q) = %d, want %d", tt.file, got, tt.want)
		}
	}
}

// The splicing logic must never corrupt a file: whatever the layout,
// the output parses and carries every document.
func TestEditOutputParses(t *testing.T) {
	files := []string{
		"---\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      containers:\n      - name: web\n        image: nginx\n",
		"# c1\n---\n# c2\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      containers:\n      - name: web\n        image: nginx\n---\nkind: Service\nmetadata:\n  name: web\n",
	}
	for _, f := range files {
		out, err := edit(t, f, Change{
			Kind: "Deployment", Name: "web", Type: typeSetPullPolicy,
			Params: map[string]string{"container": "web", "to": "IfNotPresent"},
		})
		if err != nil {
			t.Fatalf("%q: %v", f, err)
		}
		docs, err := decodeDocs([]byte(out))
		if err != nil {
			t.Fatalf("%q -> %q: %v", f, out, err)
		}
		var kinds []string
		for _, d := range docs {
			if root := docRoot(d); root != nil {
				kinds = append(kinds, child(root, "kind").Value)
			}
		}
		if len(kinds) == 0 || kinds[0] != "Deployment" {
			t.Errorf("%q -> %q: kinds = %v", f, out, kinds)
		}
	}
}
