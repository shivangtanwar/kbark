// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"

	"github.com/shivangtanwar/kbark/internal/autofix"
	"github.com/shivangtanwar/kbark/internal/gitops"
)

// gitHost adapts the GitHub client to what the engine asks of a Git
// host, so the engine does not depend on any one provider.
type gitHost struct{ client *gitops.Client }

func (g gitHost) OpenPR(ctx context.Context, r autofix.PRRequest) (autofix.PRResult, error) {
	pr, err := g.client.Open(ctx, gitops.Request{
		Source: gitops.Source{Repo: r.Source.Repo, Branch: r.Source.Branch, Path: r.Source.Path},
		Change: gitops.Change{Kind: r.Kind, Namespace: r.Namespace, Name: r.Name, Type: r.Action, Params: r.Params},
		Title:  r.Title, Body: r.Body, Head: r.Head,
	})
	if err != nil {
		return autofix.PRResult{}, err
	}
	return autofix.PRResult{Number: pr.Number, URL: pr.URL}, nil
}

func (g gitHost) PRState(ctx context.Context, repo string, number int) (string, error) {
	return g.client.State(ctx, repo, number)
}

// buildGitHost returns the Git host when a token is present in the
// named environment variable, or nil: without a token kbark simply
// does not offer pull requests.
func buildGitHost(tokenEnv, apiURL string) autofix.PullRequests {
	token := os.Getenv(tokenEnv)
	if token == "" {
		return nil
	}
	return gitHost{client: &gitops.Client{BaseURL: apiURL, Token: token}}
}
