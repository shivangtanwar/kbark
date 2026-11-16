// SPDX-License-Identifier: Apache-2.0

package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/shivangtanwar/kbark/internal/auth"
	"github.com/shivangtanwar/kbark/internal/autofix"
	"github.com/shivangtanwar/kbark/internal/incident"
)

const host = "127.0.0.1:8765"

var jsonHeader = map[string]string{"Content-Type": "application/json"}

func newServer(t *testing.T, withAuth bool) *Server {
	t.Helper()
	hub := NewHub()
	store := incident.NewStore(nil, hub.Notify)
	s := &Server{Engine: autofix.New(nil, store, autofix.Config{}, hub.Notify), Store: store, Hub: hub, AllowedHosts: LocalHosts(host)}
	if withAuth {
		h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
		m, err := auth.New([]auth.User{
			{Username: "asha", Name: "Asha Rao", Role: auth.RoleApprover, PasswordHash: string(h)},
			{Username: "vik", Name: "Vik", Role: auth.RoleViewer, PasswordHash: string(h)},
			{Username: "root", Name: "Root", Role: auth.RoleAdmin, PasswordHash: string(h)},
		})
		if err != nil {
			t.Fatal(err)
		}
		s.Auth = m
	}
	return s
}

func do(h http.Handler, method, path, hostHeader string, headers map[string]string, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = hostHeader
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func signIn(t *testing.T, h http.Handler, user string) *http.Cookie {
	t.Helper()
	rec := do(h, "POST", "/api/login", host, jsonHeader, `{"username":"`+user+`","password":"correct horse battery"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", user, rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Errorf("session cookie must be HttpOnly and SameSite=Strict: %+v", c)
			}
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

func TestConsoleIsServedWithClientSideRoutes(t *testing.T) {
	h := newServer(t, false).Handler()
	for _, path := range []string{"/", "/incidents", "/incidents/INC-0001", "/settings"} {
		rec := do(h, "GET", path, host, nil, "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Errorf("GET %s = %d, want the console shell", path, rec.Code)
		}
	}
	if rec := do(h, "GET", "/assets/missing.js", host, nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("a missing asset must be 404, not the shell: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/nope", host, nil, ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "error") {
		t.Errorf("unknown API path must be a JSON 404: %d %s", rec.Code, rec.Body)
	}
}

func TestWritesFromOtherPagesAreRefused(t *testing.T) {
	h := newServer(t, false).Handler()
	tests := []struct {
		name    string
		host    string
		headers map[string]string
		want    int
	}{
		{"same-origin JSON reaches the engine", host, map[string]string{"Content-Type": "application/json", "Origin": "http://" + host}, http.StatusNotFound},
		{"no origin header (curl) reaches the engine", host, jsonHeader, http.StatusNotFound},
		{"rebinding: foreign Host", "evil.example", jsonHeader, http.StatusForbidden},
		{"cross-site fetch: foreign Origin", host, map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"}, http.StatusForbidden},
		{"cross-site form post", host, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
		{"cross-site text/plain post", host, map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(h, "POST", "/api/incidents/INC-0001/approve", tt.host, tt.headers, `{"operator":"asha"}`)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body)
			}
		})
	}
}

func TestOpenModeNeedsAnOperatorName(t *testing.T) {
	rec := do(newServer(t, false).Handler(), "POST", "/api/incidents/INC-0001/approve", host, jsonHeader, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
	}
}

func TestSignInIsRequiredWhenEnabled(t *testing.T) {
	h := newServer(t, true).Handler()
	for _, path := range []string{"/api/state", "/api/audit", "/api/history", "/api/settings", "/api/stream", "/api/incidents/INC-0001"} {
		if rec := do(h, "GET", path, host, nil, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", path, rec.Code)
		}
	}
	if rec := do(h, "POST", "/api/incidents/INC-0001/approve", host, jsonHeader, `{"operator":"mallory"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("unsigned approve = %d, want 401", rec.Code)
	}
	// The shell and /api/me stay reachable so the sign-in page can load.
	if rec := do(h, "GET", "/", host, nil, ""); rec.Code != http.StatusOK {
		t.Errorf("console shell = %d", rec.Code)
	}
	var me meResponse
	rec := do(h, "GET", "/api/me", host, nil, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if rec.Code != http.StatusOK || !me.AuthEnabled || me.User != nil {
		t.Errorf("/api/me signed out: %d %+v", rec.Code, me)
	}
	if rec := do(h, "POST", "/api/login", host, jsonHeader, `{"username":"asha","password":"wrong"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password = %d, want 401", rec.Code)
	}
}

func TestRolesGateWrites(t *testing.T) {
	h := newServer(t, true).Handler()
	viewer, approver := signIn(t, h, "vik"), signIn(t, h, "asha")

	if rec := do(h, "GET", "/api/state", host, nil, "", viewer); rec.Code != http.StatusOK {
		t.Fatalf("viewer cannot read state: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/incidents/INC-0001/approve", host, jsonHeader, `{}`, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer approve = %d, want 403 (%s)", rec.Code, rec.Body)
	}
	// An approver gets past the role check: 404 because the incident
	// does not exist, not 400 for a missing operator, which shows the
	// operator came from the session.
	if rec := do(h, "POST", "/api/incidents/INC-0001/approve", host, jsonHeader, `{}`, approver); rec.Code != http.StatusNotFound {
		t.Fatalf("approver approve = %d, want 404 (%s)", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/logout", host, jsonHeader, `{}`, approver); rec.Code != http.StatusOK {
		t.Fatalf("logout: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/state", host, nil, "", approver); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session survived logout: %d", rec.Code)
	}
}

func TestOnlyAdminsSeeTheUserList(t *testing.T) {
	h := newServer(t, true).Handler()
	for user, want := range map[string]int{"root": 3, "asha": 0} {
		var out Settings
		rec := do(h, "GET", "/api/settings", host, nil, "", signIn(t, h, user))
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s settings: %d %v", user, rec.Code, err)
		}
		if len(out.Users) != want || out.Policy == nil {
			t.Errorf("%s sees %d users, want %d", user, len(out.Users), want)
		}
		if strings.Contains(rec.Body.String(), "$2a$") {
			t.Errorf("password hashes leaked to %s", user)
		}
	}
}

func TestLockoutSurfacesAs429(t *testing.T) {
	h := newServer(t, true).Handler()
	for i := 0; i < 5; i++ {
		do(h, "POST", "/api/login", host, jsonHeader, `{"username":"asha","password":"wrong"}`)
	}
	if rec := do(h, "POST", "/api/login", host, jsonHeader, `{"username":"asha","password":"correct horse battery"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
}

func TestLocalHosts(t *testing.T) {
	if got := LocalHosts("127.0.0.1:9000"); len(got) != 3 {
		t.Errorf("loopback: %v", got)
	}
	if got := LocalHosts("0.0.0.0:9000"); got != nil {
		t.Errorf("non-loopback must not restrict Host: %v", got)
	}
}

func TestConsoleScriptIsCompressedOnRequest(t *testing.T) {
	h := newServer(t, false).Handler()
	m := regexp.MustCompile(`/assets/index-[^"]+\.js`).FindString(do(h, "GET", "/", host, nil, "").Body.String())
	if m == "" {
		t.Skip("the console has not been built")
	}
	plain := do(h, "GET", m, host, nil, "")
	if plain.Code != http.StatusOK || plain.Header().Get("Content-Encoding") != "" {
		t.Fatalf("without Accept-Encoding: %d, encoding %q", plain.Code, plain.Header().Get("Content-Encoding"))
	}
	req := httptest.NewRequest("GET", m, nil)
	req.Host = host
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("want a gzip-encoded script, got %q %q", rec.Header().Get("Content-Encoding"), rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Error("fingerprinted assets must stay cacheable when compressed")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if len(body) != plain.Body.Len() || rec.Body.Len() >= plain.Body.Len() {
		t.Errorf("decompressed %d bytes, want %d", len(body), plain.Body.Len())
	}
}
