// SPDX-License-Identifier: Apache-2.0

package autofix

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shivangtanwar/kbark/internal/incident"
)

// PRRequest is a fix to be proposed at the source repository.
type PRRequest struct {
	Source incident.Source
	// The object and the change, as kbark's own action describes them.
	Kind, Namespace, Name string
	Action                string
	Params                map[string]string
	Title, Body, Head     string
}

// PRResult is the pull request that was opened.
type PRResult struct {
	Number int
	URL    string
}

// PullRequests is the Git host. The engine depends on this interface,
// not on a provider, so tests can stand in for GitHub.
type PullRequests interface {
	OpenPR(ctx context.Context, r PRRequest) (PRResult, error)
	// PRState reports "open", "merged" or "closed".
	PRState(ctx context.Context, repo string, number int) (string, error)
}

// ErrNoSource is returned when a pull request is asked for but the
// proposal has no Git source or no Git host is configured.
var ErrNoSource = errors.New("this fix cannot be sent as a pull request: the object declares no Git source, the fix is not a manifest edit, or kbark has no Git token")

// OpenPullRequest sends the proposed fix to the object's source
// repository instead of applying it. It is a person's decision, like
// approval, and nothing is written to the cluster.
func (e *Engine) OpenPullRequest(ctx context.Context, id, operator string) (*incident.Incident, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return nil, ErrOperatorRequired
	}
	inc, err := e.store.Get(id)
	if err != nil {
		return nil, err
	}
	if inc.State != incident.StateProposed || inc.Action == nil {
		return nil, incident.ErrWrongState
	}
	if e.cfg.PullRequests == nil || inc.Action.Source == nil {
		return nil, ErrNoSource
	}
	a := inc.Action
	req := PRRequest{
		Source: *a.Source, Kind: a.TargetKind, Namespace: inc.Namespace, Name: a.TargetName,
		Action: a.Type, Params: a.Params,
		Title: "kbark: " + a.Title,
		Head:  fmt.Sprintf("kbark/%s-%s", strings.ToLower(inc.ID), a.Type),
		Body:  prBody(inc, operator, e.cfg.PublicURL),
	}
	pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	res, err := e.cfg.PullRequests.OpenPR(pctx, req)
	cancel()
	if err != nil {
		e.store.Note(id, "pull-request-failed", operator, "a pull request could not be opened: "+err.Error(), time.Now())
		return nil, fmt.Errorf("pull request: %w", err)
	}
	return e.store.Update(id, time.Now(), func(i *incident.Incident) (incident.State, string, string, error) {
		if i.State != incident.StateProposed {
			return "", "", "", incident.ErrWrongState
		}
		i.PullRequest = &incident.PullRequest{Repo: a.Source.Repo, Number: res.Number, URL: res.URL, State: "open"}
		return incident.StatePullRequest, operator, fmt.Sprintf("pull request #%d opened at %s for: %s. Nothing was changed in the cluster.", res.Number, a.Source.Repo, a.Title), nil
	})
}

// prBody is the description a reviewer of the pull request reads. It
// carries the evidence, not the AI explanation: the reviewer should be
// able to check every statement in it against the cluster.
func prBody(inc *incident.Incident, operator, publicURL string) string {
	var b strings.Builder
	a := inc.Action
	fmt.Fprintf(&b, "kbark detected a failure on `%s` in namespace `%s` of cluster `%s` and proposes this change at the source, so the cluster does not drift from Git.\n\n", strings.ToLower(inc.Workload.String()), inc.Namespace, inc.Cluster)
	fmt.Fprintf(&b, "**Failure:** %s\n\n", inc.Summary)
	b.WriteString("**Observed:**\n")
	for _, ev := range inc.Evidence {
		fmt.Fprintf(&b, "- %s: `%s`\n", ev.Label, strings.ReplaceAll(ev.Value, "`", "'"))
	}
	fmt.Fprintf(&b, "\n**Change:** %s\n\n%s\n", a.Title, a.Rationale)
	for _, w := range a.Warnings {
		if !strings.Contains(w, "drift from its source") {
			fmt.Fprintf(&b, "\n> %s\n", w)
		}
	}
	fmt.Fprintf(&b, "\nRequested by %s through kbark (incident %s", operator, inc.ID)
	if publicURL != "" {
		fmt.Fprintf(&b, ", %s/incidents/%s", strings.TrimRight(publicURL, "/"), inc.ID)
	}
	b.WriteString("). kbark changed nothing in the cluster; the incident closes when the failure clears after this is merged and synced.\n")
	return b.String()
}

// checkPullRequest follows up on an open pull request: a merge is
// noted once, and a pull request closed without merging hands the
// incident back for a fresh decision.
func (e *Engine) checkPullRequest(ctx context.Context, inc *incident.Incident) {
	if e.cfg.PullRequests == nil || inc.PullRequest == nil || inc.PullRequest.State == "merged" {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	state, err := e.cfg.PullRequests.PRState(pctx, inc.PullRequest.Repo, inc.PullRequest.Number)
	cancel()
	if err != nil || state == inc.PullRequest.State || state == "open" {
		return
	}
	now := time.Now()
	switch state {
	case "merged":
		_, _ = e.store.Update(inc.ID, now, func(i *incident.Incident) (incident.State, string, string, error) {
			if i.PullRequest != nil {
				i.PullRequest.State = "merged"
			}
			return "", "", "", nil
		})
		e.store.Note(inc.ID, "pull-request-merged", "kbark", fmt.Sprintf("pull request #%d was merged; waiting for the change to reach the cluster", inc.PullRequest.Number), now)
	case "closed":
		_, err := e.store.Update(inc.ID, now, func(i *incident.Incident) (incident.State, string, string, error) {
			if i.State != incident.StatePullRequest {
				return "", "", "", incident.ErrWrongState
			}
			if i.PullRequest != nil {
				i.PullRequest.State = "closed"
			}
			return incident.StateProposed, "kbark", fmt.Sprintf("pull request #%d was closed without merging; the fix is proposed again", inc.PullRequest.Number), nil
		})
		if err == nil {
			e.plan(ctx, inc.ID, "kbark")
		}
	}
}
