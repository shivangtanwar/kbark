// SPDX-License-Identifier: Apache-2.0

package kube_test

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/shivangtanwar/kbark/internal/kube"
)

const fixtureKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://10.0.0.1:6443
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: redacted
`

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(fixtureKubeconfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestRESTConfig_resolvesCurrentContext(t *testing.T) {
	path := writeFixture(t)
	flags := genericclioptions.NewConfigFlags(true)
	flags.KubeConfig = &path

	cfg, err := kube.RESTConfig(flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "https://10.0.0.1:6443" {
		t.Errorf("host = %q, want %q", cfg.Host, "https://10.0.0.1:6443")
	}
}

func TestRESTConfig_missingContextReturnsError(t *testing.T) {
	path := writeFixture(t)
	flags := genericclioptions.NewConfigFlags(true)
	flags.KubeConfig = &path
	missing := "does-not-exist"
	flags.Context = &missing

	if _, err := kube.RESTConfig(flags); err == nil {
		t.Fatal("expected error for missing context, got nil")
	}
}
