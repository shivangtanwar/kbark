// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"fmt"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/shivangtanwar/kbark/internal/kube"
)

func checkKubeconfig(flags *genericclioptions.ConfigFlags) (Result, *rest.Config) {
	cfg, err := kube.RESTConfig(flags)
	if err != nil {
		return Result{Name: "kubeconfig", Status: Red, Detail: err.Error()}, nil
	}
	host := cfg.Host
	if host == "" {
		host = "<empty>"
	}
	return Result{Name: "kubeconfig", Status: Green, Detail: host}, cfg
}

func checkAPIServer(_ context.Context, cfg *rest.Config) Result {
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return Result{Name: "apiserver", Status: Red, Detail: fmt.Sprintf("build client: %v", err)}
	}
	info, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return Result{Name: "apiserver", Status: Red, Detail: err.Error()}
	}
	return Result{
		Name:   "apiserver",
		Status: Green,
		Detail: fmt.Sprintf("%s on %s", info.GitVersion, info.Platform),
	}
}
