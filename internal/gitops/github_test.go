// SPDX-License-Identifier: Apache-2.0

package gitops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The manifest served by the fake GitHub.
var fakeManifest = []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      containers:
      - name: web
        image: nginx
        resources:
          limits:
            memory: 64Mi
          requests:
            memory: 64Mi
`)

// ghServer fakes the REST endpoints Open touches and records every
// request in order.
type ghServer struct {
	mu       sync.Mutex
	requests []string // "METHOD path?query"
	// refExists reports whether the head branch already exists.
	refExists bool
	// existingPR, when nonzero, is returned for the open-PR search.
	existingPR int
	// failOn, when set, makes matching paths answer with failCode
	// and failBody.
	failOn   string
	failBody string
	failCode int
	bearer   string
	server   *httptest.Server
	// stateOverride, when set, replaces the pull's state/merged in
	// GET /repos/acme/pay/pulls/7.
	stateOverride string
	merged        bool
	committed     struct {
		message, content, sha, branch string // recorded from PUT
	}
	prCreated struct{ title, body, head, base string }
}

func newGHServer(t *testing.T) *ghServer {
	t.Helper()
	g := &ghServer{}
	mux := http.NewServeMux()
	// One dispatcher for the repository root and every path under it,
	// so no two ServeMux patterns collide.
	dispatch := func(w http.ResponseWriter, r *http.Request) {
		g.record(r)
		if g.failOn != "" && strings.HasPrefix(r.URL.Path, g.failOn) {
			writeJSONStatus(w, g.failCode, map[string]string{"message": g.failBody})
			return
		}
		g.checkHeaders(w, r)
		switch p := r.URL.Path; {
		case p == "/repos/acme/pay" && r.Method == http.MethodGet:
			writeJSON(w, map[string]any{"default_branch": "main", "full_name": "acme/pay"})
		case p == "/repos/acme/pay/contents/deploy/web.yaml" && r.Method == http.MethodGet:
			if r.URL.Query().Get("ref") == "" {
				http.Error(w, "missing ref", http.StatusBadRequest)
				return
			}
			writeJSON(w, map[string]any{
				"sha":      "blob123",
				"encoding": "base64",
				// GitHub wraps base64 with line breaks; the client
				// must strip them.
				"content": insertBreaks(base64.StdEncoding.EncodeToString(fakeManifest)),
			})
		case p == "/repos/acme/pay/git/ref/heads/main" && r.Method == http.MethodGet:
			writeJSON(w, map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": "headabc"}})
		case p == "/repos/acme/pay/git/refs" && r.Method == http.MethodPost:
			var body struct{ Ref, SHA string }
			json.NewDecoder(r.Body).Decode(&body)
			if g.refExists {
				writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"message": "Reference already exists"})
				return
			}
			if body.Ref != "refs/heads/kbark/head" || body.SHA != "headabc" {
				http.Error(w, "bad ref body", http.StatusBadRequest)
				return
			}
			writeJSONStatus(w, http.StatusCreated, map[string]string{"ref": body.Ref})
		case p == "/repos/acme/pay/pulls" && r.Method == http.MethodGet:
			if q := r.URL.Query(); q.Get("head") != "acme:kbark/head" || q.Get("state") != "open" {
				http.Error(w, "bad pull search", http.StatusBadRequest)
				return
			}
			prs := []map[string]any{}
			if g.existingPR != 0 {
				prs = append(prs, map[string]any{
					"number": g.existingPR, "html_url": "https://github.com/acme/pay/pull/" + fmt.Sprint(g.existingPR),
					"head": map[string]string{"ref": "kbark/head"},
				})
			}
			writeJSON(w, prs)
		case p == "/repos/acme/pay/contents/deploy/web.yaml" && r.Method == http.MethodPut:
			var body struct{ Message, Content, SHA, Branch string }
			json.NewDecoder(r.Body).Decode(&body)
			g.mu.Lock()
			g.committed.message, g.committed.content, g.committed.sha, g.committed.branch = body.Message, body.Content, body.SHA, body.Branch
			g.mu.Unlock()
			writeJSON(w, map[string]any{"content": map[string]string{"sha": "blob456"}})
		case p == "/repos/acme/pay/pulls" && r.Method == http.MethodPost:
			var body struct{ Title, Body, Head, Base string }
			json.NewDecoder(r.Body).Decode(&body)
			g.mu.Lock()
			g.prCreated.title, g.prCreated.body, g.prCreated.head, g.prCreated.base = body.Title, body.Body, body.Head, body.Base
			g.mu.Unlock()
			writeJSONStatus(w, http.StatusCreated, map[string]any{
				"number": 7, "html_url": "https://github.com/acme/pay/pull/7",
				"head": map[string]string{"ref": "kbark/head"},
			})
		case p == "/repos/acme/pay/pulls/7" && r.Method == http.MethodGet:
			if st, ok := g.pullState(); ok {
				g.mu.Lock()
				merged := g.merged
				g.mu.Unlock()
				writeJSON(w, map[string]any{"number": 7, "state": st, "merged": merged})
				return
			}
			writeJSON(w, map[string]any{"number": 7, "state": "open", "merged": false})
		default:
			http.NotFound(w, r)
		}
	}
	mux.HandleFunc("/repos/acme/pay", dispatch)
	mux.HandleFunc("/repos/acme/pay/", dispatch)
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// insertBreaks wraps a base64 string with newlines every 60 chars,
// the way GitHub does.
func insertBreaks(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i += 60 {
		end := i + 60
		if end > len(s) {
			end = len(s)
		}
		b.WriteString(s[i:end])
		b.WriteByte('\n')
	}
	return b.String()
}

func (g *ghServer) reqs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.requests...)
}

// record appends one request to the log and remembers the bearer.
func (g *ghServer) record(r *http.Request) {
	g.mu.Lock()
	g.requests = append(g.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	g.bearer = r.Header.Get("Authorization")
	g.mu.Unlock()
}

// checkHeaders rejects requests without the headers GitHub requires.
func (g *ghServer) checkHeaders(w http.ResponseWriter, r *http.Request) {
	if v := r.Header.Get("X-GitHub-Api-Version"); v != "2022-11-28" {
		http.Error(w, "bad api version", http.StatusBadRequest)
		return
	}
	if a := r.Header.Get("Accept"); a != "application/vnd.github+json" {
		http.Error(w, "bad accept", http.StatusBadRequest)
		return
	}
}

func (g *ghServer) pullState() (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stateOverride, g.stateOverride != ""
}

func (g *ghServer) tokenSeen(tok string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bearer == "Bearer "+tok
}

func (g *ghServer) tokenLeaked(err error, tok string) bool {
	return err != nil && strings.Contains(err.Error(), tok)
}

const testToken = "ghp_secret_token_123"

func testClient(g *ghServer) *Client {
	return &Client{BaseURL: g.server.URL, Token: testToken, HTTP: g.server.Client()}
}

func openRequest() Request {
	return Request{
		Source: Source{Repo: "acme/pay", Branch: "main", Path: "deploy/web.yaml"},
		Change: Change{
			Kind: "Deployment", Name: "web", Type: typeSetMemoryLimit,
			Params: map[string]string{"container": "web", "to": "128Mi"},
		},
		Title: "Raise memory limit of container \"web\" from 64Mi to 128Mi",
		Body:  "Proposed by kbark after OOMKills; approve to apply.",
		Head:  "kbark/head",
	}
}

func TestOpenHappyPath(t *testing.T) {
	g := newGHServer(t)
	pr, err := testClient(g).Open(context.Background(), openRequest())
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || pr.URL != "https://github.com/acme/pay/pull/7" || pr.Head != "kbark/head" {
		t.Errorf("pull request = %+v", pr)
	}
	if !g.tokenSeen(testToken) {
		t.Errorf("Authorization: Bearer header not sent")
	}

	// The committed content is the edited file, base64-decoded, on
	// the head branch, with the blob sha and the title as message.
	g.mu.Lock()
	msg, content, sha, branch := g.committed.message, g.committed.content, g.committed.sha, g.committed.branch
	title, body, head, base := g.prCreated.title, g.prCreated.body, g.prCreated.head, g.prCreated.base
	g.mu.Unlock()
	if msg != openRequest().Title {
		t.Errorf("commit message = %q, want the title %q", msg, openRequest().Title)
	}
	if sha != "blob123" {
		t.Errorf("commit sha = %q, want blob123", sha)
	}
	if branch != "kbark/head" {
		t.Errorf("commit branch = %q, want kbark/head", branch)
	}
	dec, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		t.Fatalf("committed content is not base64: %v", err)
	}
	if want, err := Edit(fakeManifest, openRequest().Change); err != nil || string(dec) != string(want) {
		t.Errorf("committed content is not the edited file:\n%q\nwant:\n%q (err %v)", dec, want, err)
	}
	if title != openRequest().Title || body != openRequest().Body || head != "kbark/head" || base != "main" {
		t.Errorf("pull request = title %q body %q head %q base %q", title, body, head, base)
	}

	// Order: the file is read and edited before any write happens.
	reqs := g.reqs()
	firstWrite := -1
	for i, r := range reqs {
		if strings.HasPrefix(r, "POST /") || strings.HasPrefix(r, "PUT /") {
			firstWrite = i
			break
		}
	}
	if firstWrite < 2 {
		t.Errorf("a write happened before the file was read and edited: %v", reqs)
	}
	wantReqs := []string{
		"GET /repos/acme/pay/contents/deploy/web.yaml?ref=main",
		"GET /repos/acme/pay/git/ref/heads/main?",
		"POST /repos/acme/pay/git/refs?",
		"PUT /repos/acme/pay/contents/deploy/web.yaml?",
		"POST /repos/acme/pay/pulls?",
	}
	if len(reqs) != len(wantReqs) {
		t.Fatalf("requests = %v, want %v", reqs, wantReqs)
	}
	for i := range wantReqs {
		if reqs[i] != wantReqs[i] {
			t.Errorf("request %d = %q, want %q", i, reqs[i], wantReqs[i])
		}
	}
}

func TestOpenDefaultBranch(t *testing.T) {
	g := newGHServer(t)
	r := openRequest()
	r.Source.Branch = ""
	pr, err := testClient(g).Open(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 {
		t.Errorf("pull request = %+v", pr)
	}
	reqs := g.reqs()
	if len(reqs) == 0 || reqs[0] != "GET /repos/acme/pay?" {
		t.Errorf("first request = %v, want GET /repos/acme/pay (default-branch resolution)", reqs)
	}
	found := false
	for _, r := range reqs {
		if r == "GET /repos/acme/pay/contents/deploy/web.yaml?ref=main" {
			found = true
		}
	}
	if !found {
		t.Errorf("file was not read from the resolved default branch: %v", reqs)
	}
	if g.prCreated.base != "main" {
		t.Errorf("pull request base = %q, want main", g.prCreated.base)
	}
}

func TestOpenExistingBranchWithOpenPR(t *testing.T) {
	g := newGHServer(t)
	g.refExists = true
	g.existingPR = 3
	pr, err := testClient(g).Open(context.Background(), openRequest())
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 3 || pr.URL != "https://github.com/acme/pay/pull/3" || pr.Head != "kbark/head" {
		t.Errorf("reused pull request = %+v", pr)
	}
	// Nothing was committed and no new pull request was opened.
	g.mu.Lock()
	committed, prTitle := g.committed.content, g.prCreated.title
	g.mu.Unlock()
	if committed != "" || prTitle != "" {
		t.Errorf("unexpected write: committed %q, opened %q", committed, prTitle)
	}
}

func TestOpenExistingBranchWithoutPR(t *testing.T) {
	g := newGHServer(t)
	g.refExists = true
	_, err := testClient(g).Open(context.Background(), openRequest())
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "kbark/head") || !strings.Contains(err.Error(), "no pull request") {
		t.Errorf("error = %v, want one naming the branch and the missing pull request", err)
	}
	if g.tokenLeaked(err, testToken) {
		t.Errorf("error contains the token: %v", err)
	}
}

func TestOpenAPIError(t *testing.T) {
	g := newGHServer(t)
	g.failOn = "/repos/acme/pay/contents/"
	g.failCode = 404
	g.failBody = "Not Found"
	_, err := testClient(g).Open(context.Background(), openRequest())
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("error = %v, want the status and GitHub's message", err)
	}
	if g.tokenLeaked(err, testToken) {
		t.Errorf("error contains the token: %v", err)
	}
	// Nothing was written before the failure.
	for _, r := range g.reqs() {
		if strings.HasPrefix(r, "POST ") || strings.HasPrefix(r, "PUT ") {
			t.Errorf("a write happened despite the failure: %s", r)
		}
	}
}

func TestOpenInvalidRepoAndPath(t *testing.T) {
	g := newGHServer(t)
	bad := []Request{
		{Source: Source{Repo: "acme", Branch: "main", Path: "deploy/web.yaml"}, Head: "kbark/head"},
		{Source: Source{Repo: "acme/pay/extra", Branch: "main", Path: "deploy/web.yaml"}, Head: "kbark/head"},
		{Source: Source{Repo: "/pay", Branch: "main", Path: "deploy/web.yaml"}, Head: "kbark/head"},
		{Source: Source{Repo: "acme/", Branch: "main", Path: "deploy/web.yaml"}, Head: "kbark/head"},
		{Source: Source{Repo: "acme/pay", Branch: "main", Path: "/deploy/web.yaml"}, Head: "kbark/head"},
		{Source: Source{Repo: "acme/pay", Branch: "main", Path: "../etc/passwd"}, Head: "kbark/head"},
		{Source: Source{Repo: "acme/pay", Branch: "main", Path: "deploy/../../etc/passwd"}, Head: "kbark/head"},
		{Source: Source{Repo: "acme/pay", Branch: "main", Path: ""}, Head: "kbark/head"},
		{Source: Source{Repo: "acme/pay", Branch: "main", Path: "deploy/web.yaml"}, Head: ""},
	}
	for _, r := range bad {
		_, err := testClient(g).Open(context.Background(), r)
		if err == nil {
			t.Errorf("Open accepted %+v", r.Source)
			continue
		}
		if g.tokenLeaked(err, testToken) {
			t.Errorf("error contains the token: %v", err)
		}
	}
	if reqs := g.reqs(); len(reqs) != 0 {
		t.Errorf("requests were made for invalid input: %v", reqs)
	}
}

func TestOpenEditFailureWritesNothing(t *testing.T) {
	g := newGHServer(t)
	// The manifest has no object for this change, so Edit fails.
	r := openRequest()
	r.Change = Change{
		Kind: "Deployment", Name: "other", Type: typeSetMemoryLimit,
		Params: map[string]string{"container": "web", "to": "128Mi"},
	}
	_, err := testClient(g).Open(context.Background(), r)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	for _, req := range g.reqs() {
		if strings.HasPrefix(req, "POST ") || strings.HasPrefix(req, "PUT ") {
			t.Errorf("a write happened although Edit failed: %s", req)
		}
	}
}

func TestState(t *testing.T) {
	tests := []struct {
		name    string
		state   string
		merged  bool
		want    string
		wantErr bool
	}{
		{name: "open", state: "open", want: "open"},
		{name: "merged", state: "closed", merged: true, want: "merged"},
		{name: "closed", state: "closed", want: "closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newGHServer(t)
			if tt.wantErr {
				g.failOn = "/repos/acme/pay/pulls/7"
				g.failCode = 404
				g.failBody = "Not Found"
			} else {
				g.stateOverride, g.merged = tt.state, tt.merged
			}
			got, err := testClient(g).State(context.Background(), "acme/pay", 7)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("State = %q, %v; want error", got, err)
				}
				if !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "Not Found") {
					t.Errorf("error = %v, want status and message", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("State = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStateInvalidRepo(t *testing.T) {
	g := newGHServer(t)
	if _, err := testClient(g).State(context.Background(), "acme", 7); err == nil {
		t.Errorf("State accepted a bad repo")
	}
	if reqs := g.reqs(); len(reqs) != 0 {
		t.Errorf("requests were made for invalid repo: %v", reqs)
	}
}

func TestTokenNeverInErrors(t *testing.T) {
	g := newGHServer(t)
	// A 500 whose body accidentally embeds everything the server saw.
	g.failOn = "/repos/"
	g.failCode = 500
	g.failBody = "internal error for Bearer " + testToken
	c := &Client{BaseURL: g.server.URL, Token: testToken, HTTP: g.server.Client()}
	r := openRequest()
	_, err := c.Open(context.Background(), r)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	// GitHub's message came back with the bearer string; the client
	// must not have put the token into the error itself beyond what
	// the server chose to say. The contract under test is that the
	// client never adds it.
	if strings.Count(err.Error(), testToken) > 1 {
		t.Errorf("error repeats the token: %v", err)
	}
	// The ordinary case: the message is clean, so the error is.
	g.failBody = "internal error"
	_, err = c.Open(context.Background(), r)
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if g.tokenLeaked(err, testToken) {
		t.Errorf("error contains the token: %v", err)
	}
}

func TestPathEscaping(t *testing.T) {
	// A path with a space and an ampersand must survive as one
	// contents path, not be split into extra segments or query
	// parameters.
	var gotContents string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/pay", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"default_branch": "main"})
	})
	mux.HandleFunc("/repos/acme/pay/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/acme/pay/contents/"):
			gotContents = r.URL.Path
			if r.Method == http.MethodGet {
				writeJSON(w, map[string]any{
					"sha": "blob123", "encoding": "base64",
					"content": base64.StdEncoding.EncodeToString(fakeManifest),
				})
				return
			}
			writeJSON(w, map[string]any{"content": map[string]string{"sha": "blob456"}})
		case r.URL.Path == "/repos/acme/pay/git/ref/heads/main":
			writeJSON(w, map[string]any{"object": map[string]string{"sha": "headabc"}})
		case r.URL.Path == "/repos/acme/pay/git/refs":
			writeJSONStatus(w, http.StatusCreated, map[string]string{"ref": "refs/heads/kbark/head"})
		case r.URL.Path == "/repos/acme/pay/pulls" && r.Method == http.MethodPost:
			writeJSONStatus(w, http.StatusCreated, map[string]any{
				"number": 7, "html_url": "u", "head": map[string]string{"ref": "kbark/head"},
			})
		default:
			http.NotFound(w, r)
		}
	})
	g := &ghServer{server: httptest.NewServer(mux)}
	t.Cleanup(g.server.Close)
	c := &Client{BaseURL: g.server.URL, Token: testToken, HTTP: g.server.Client()}
	r := openRequest()
	r.Source.Path = "deploy/a b/c&d.yaml"
	if _, err := c.Open(context.Background(), r); err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The server sees the decoded path, with the space and ampersand
	// intact and no query contamination.
	if gotContents != "/repos/acme/pay/contents/deploy/a b/c&d.yaml" {
		t.Errorf("contents path = %q", gotContents)
	}
}
