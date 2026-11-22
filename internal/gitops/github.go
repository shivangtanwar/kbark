// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultAPI = "https://api.github.com"
	apiVersion = "2022-11-28"
	// timeout matches the command's other outbound calls.
	timeout = 20 * time.Second
)

// Source is where a manifest lives: Repo is "owner/name", Branch is
// the pull request's base branch ("" resolves the repository's
// default branch), and Path is the file to edit, relative to the
// repository root.
type Source struct {
	Repo   string
	Branch string
	Path   string
}

// Client talks to the GitHub REST API: api.github.com by default, so
// a GitHub Enterprise base URL or a test server can be substituted.
// The token is only ever sent in the Authorization header; it is not
// part of any error or log.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Request is one pull request to open.
type Request struct {
	Source Source
	Change Change
	// Title is both the pull request title and the commit message.
	Title string
	Body  string
	// Head is the branch to create, e.g. "kbark/inc-0012-set-memory-limit".
	Head string
}

// PullRequest is a pull request that exists on GitHub.
type PullRequest struct {
	Number int
	// URL is the pull request's html_url.
	URL  string
	Head string
}

// Open resolves r.Source, applies r.Change to the manifest there,
// commits the result to a new branch and opens a pull request from
// it. If the branch exists, it is reused only when a pull request
// from it is already open, and that pull request is returned instead;
// a fresh write never lands on a branch that cannot be reused. When
// Edit fails, nothing has been written to GitHub.
func (c *Client) Open(ctx context.Context, r Request) (*PullRequest, error) {
	if _, _, err := splitRepo(r.Source.Repo); err != nil {
		return nil, err
	}
	if err := validPath(r.Source.Path); err != nil {
		return nil, err
	}
	if r.Head == "" {
		return nil, fmt.Errorf("head branch name is empty")
	}
	base := r.Source.Branch
	if base == "" {
		var err error
		base, err = c.defaultBranch(ctx, r.Source.Repo)
		if err != nil {
			return nil, err
		}
	}
	var file contentsFile
	if err := c.getJSON(ctx, fmt.Sprintf("repos/%s/contents/%s?ref=%s", escapeRepo(r.Source.Repo), escapePath(r.Source.Path), url.QueryEscape(base)), &file); err != nil {
		return nil, err
	}
	raw, err := file.decode()
	if err != nil {
		return nil, err
	}
	edited, err := Edit(raw, r.Change)
	if err != nil {
		return nil, err
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("repos/%s/git/ref/heads/%s", escapeRepo(r.Source.Repo), url.PathEscape(base)), &ref); err != nil {
		return nil, err
	}
	created, err := c.createRef(ctx, r.Source.Repo, r.Head, ref.Object.SHA)
	if err != nil {
		return nil, err
	}
	if !created {
		// The branch is already there. Reuse it only for an
		// idempotent re-approval: a pull request from it is
		// already open, and it is returned untouched.
		return c.openPullFrom(ctx, r.Source.Repo, r.Head)
	}
	body, err := json.Marshal(struct {
		Message string `json:"message"`
		Content string `json:"content"`
		SHA     string `json:"sha"`
		Branch  string `json:"branch"`
	}{r.Title, base64.StdEncoding.EncodeToString(edited), file.SHA, r.Head})
	if err != nil {
		return nil, err
	}
	if err := c.do(ctx, http.MethodPut, fmt.Sprintf("repos/%s/contents/%s", escapeRepo(r.Source.Repo), escapePath(r.Source.Path)), body, nil); err != nil {
		return nil, err
	}
	return c.openPull(ctx, r.Source, r.Head, base, r.Title, r.Body)
}

// State reports "open", "merged" or "closed" for a pull request.
func (c *Client) State(ctx context.Context, repo string, number int) (string, error) {
	if _, _, err := splitRepo(repo); err != nil {
		return "", err
	}
	var pr struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("repos/%s/pulls/%d", escapeRepo(repo), number), &pr); err != nil {
		return "", err
	}
	if pr.Merged {
		return "merged", nil
	}
	return pr.State, nil
}

// defaultBranch reads the branch to open the pull request against
// when Source.Branch is empty.
func (c *Client) defaultBranch(ctx context.Context, repo string) (string, error) {
	var repoInfo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("repos/%s", escapeRepo(repo)), &repoInfo); err != nil {
		return "", err
	}
	if repoInfo.DefaultBranch == "" {
		return "", fmt.Errorf("repository %q has no default branch", repo)
	}
	return repoInfo.DefaultBranch, nil
}

// createRef points a new branch at sha. It returns false, nil when
// the branch already exists (GitHub answers 422); false, err on any
// other failure; true, nil when it was created.
func (c *Client) createRef(ctx context.Context, repo, branch, sha string) (bool, error) {
	body, err := json.Marshal(struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	}{"refs/heads/" + branch, sha})
	if err != nil {
		return false, err
	}
	var out struct {
		Ref string `json:"ref"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/git/refs", escapeRepo(repo)), body, &out); err != nil {
		if e, ok := err.(*apiError); ok && e.status == http.StatusUnprocessableEntity {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// openPullFrom looks for an already-open pull request from branch
// and returns it, the idempotent re-approval path.
func (c *Client) openPullFrom(ctx context.Context, repo, branch string) (*PullRequest, error) {
	owner, _, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	var list []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	err = c.getJSON(ctx, fmt.Sprintf("repos/%s/pulls?head=%s&state=open", escapeRepo(repo), url.QueryEscape(owner+":"+branch)), &list)
	if err != nil {
		return nil, err
	}
	for _, pr := range list {
		if pr.Head.Ref == branch {
			return &PullRequest{Number: pr.Number, URL: pr.HTMLURL, Head: pr.Head.Ref}, nil
		}
	}
	return nil, fmt.Errorf("branch %q already exists on %s but no pull request from it is open, so kbark will not write to it; pick another head branch name", branch, repo)
}

// openPull creates the pull request.
func (c *Client) openPull(ctx context.Context, src Source, head, base, title, body string) (*PullRequest, error) {
	bodyJSON, err := json.Marshal(struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		Head  string `json:"head"`
		Base  string `json:"base"`
	}{title, body, head, base})
	if err != nil {
		return nil, err
	}
	var pr struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("repos/%s/pulls", escapeRepo(src.Repo)), bodyJSON, &pr); err != nil {
		return nil, err
	}
	return &PullRequest{Number: pr.Number, URL: pr.HTMLURL, Head: pr.Head.Ref}, nil
}

// --- plumbing ---

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return defaultAPI
	}
	return strings.TrimSuffix(c.BaseURL, "/")
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: timeout}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// do issues one request against the REST API and decodes a 2xx
// answer into out. A non-2xx answer becomes an *apiError holding the
// status and GitHub's message.
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL()+"/"+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := string(data)
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		return &apiError{status: resp.StatusCode, message: msg}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// apiError is a non-2xx answer from the API.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("github: %s: %s", statusText(e.status), e.message)
}

func statusText(code int) string {
	if s := http.StatusText(code); s != "" {
		return fmt.Sprintf("%d %s", code, s)
	}
	return fmt.Sprintf("HTTP %d", code)
}

// contentsFile is GET /repos/{repo}/contents/{path}.
type contentsFile struct {
	SHA      string `json:"sha"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

// decode returns the file bytes; GitHub base64-wraps its payload with
// line breaks.
func (f *contentsFile) decode() ([]byte, error) {
	if f.Encoding != "" && f.Encoding != "base64" {
		return nil, fmt.Errorf("file is stored as %q, not base64", f.Encoding)
	}
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', ' ', '\t':
			return -1
		}
		return r
	}, f.Content)
	b, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("decode file contents: %w", err)
	}
	return b, nil
}

// splitRepo validates "owner/name".
func splitRepo(repo string) (string, string, error) {
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("repository %q is not owner/name", repo)
	}
	return owner, name, nil
}

// validPath rejects absolute paths and parent-directory hops.
func validPath(path string) error {
	if path == "" {
		return fmt.Errorf("path is empty")
	}
	if strings.HasPrefix(path, "/") {
		return fmt.Errorf("path %q is absolute", path)
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return fmt.Errorf("path %q contains \"..\"", path)
		}
	}
	return nil
}

// escapeRepo turns "owner/name" into escaped path segments.
func escapeRepo(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// escapePath escapes each segment of a path that may contain slashes.
func escapePath(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
