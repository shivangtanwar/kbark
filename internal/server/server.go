// SPDX-License-Identifier: Apache-2.0

// Package server exposes the autofix engine over HTTP: a JSON API, a
// server-sent-events stream that says "something changed", and the
// embedded web console.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/shivangtanwar/kbark/internal/auth"
	"github.com/shivangtanwar/kbark/internal/autofix"
	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
	"github.com/shivangtanwar/kbark/internal/notify"
	"github.com/shivangtanwar/kbark/internal/policy"
)

// dist is the built web console (see web/ at the repository root).
//
//go:embed all:dist
var distFS embed.FS

const sessionCookie = "kbark_session"

// Hub fans a "changed" signal out to every connected console.
type Hub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// NewHub builds an empty hub.
func NewHub() *Hub { return &Hub{subs: map[chan struct{}]struct{}{}} }

// Notify wakes every subscriber. Never blocks.
func (h *Hub) Notify() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *Hub) subscribe() chan struct{} {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// Info is static information shown in the console.
type Info struct {
	Version   string `json:"version"`
	AI        string `json:"ai"` // "" when no provider is configured
	AuditPath string `json:"auditPath"`
	StatePath string `json:"statePath"`
	PublicURL string `json:"publicURL"`
}

// Server is the HTTP front end.
type Server struct {
	Engine *autofix.Engine
	Store  *incident.Store
	Audit  *incident.Audit
	Hub    *Hub
	Info   Info
	// Auth, when set, requires sign-in. When nil the console is open
	// and the operator name is whatever the caller supplies; serve
	// allows that only on a loopback address.
	Auth   *auth.Manager
	Notify *notify.Webhook
	// AllowedHosts, when non-empty, is the set of Host header values
	// the API answers to. It defeats DNS-rebinding attacks.
	AllowedHosts []string
	// SecureCookies marks the session cookie Secure (HTTPS only).
	SecureCookies bool
	// TrustProxy takes the client address from the reverse proxy's
	// X-Real-IP / X-Forwarded-For headers. Set it only when every
	// request really does arrive through that proxy; otherwise a
	// caller could forge the header to dodge the sign-in lockout.
	TrustProxy bool
}

// LocalHosts returns the Host values that reach a loopback listener on
// addr, or nil when addr is not loopback (no Host restriction then).
func LocalHosts(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil
		}
	}
	return []string{"127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port}
}

type ctxKey int

const userKey ctxKey = 0

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/audit", s.audit)
	mux.HandleFunc("GET /api/history", s.history)
	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("GET /api/stream", s.stream)
	mux.HandleFunc("GET /api/incidents/{id}", s.incident)
	mux.HandleFunc("POST /api/incidents/{id}/{verb}", s.act)
	mux.Handle("GET /", s.static())
	return s.guard(s.authenticate(mux))
}

// static serves the built console. Paths that are not files fall back
// to index.html so the single-page app can own its own routes.
func (s *Server) static() http.Handler {
	dist, _ := fs.Sub(distFS, "dist")
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(dist, name); err != nil {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		if strings.HasPrefix(name, "assets/") {
			// Vite fingerprints these file names, so they never change.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if name == "index.html" {
			body, err := fs.ReadFile(dist, "index.html")
			if err != nil {
				http.Error(w, "the web console has not been built; run `task ui`", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(body)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// guard rejects requests that could only come from another web page:
// a foreign Host (DNS rebinding), a foreign Origin, or a write that is
// not JSON (a cross-site form post cannot send application/json
// without a preflight, which this server never grants).
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.AllowedHosts) > 0 && !contains(s.AllowedHosts, r.Host) {
			writeError(w, http.StatusForbidden, "unexpected Host header")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
				writeError(w, http.StatusForbidden, "cross-origin requests are not allowed")
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "writes must be application/json")
				return
			}
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// authenticate attaches the signed-in user to the request and turns
// away API calls without a session when sign-in is required.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Auth == nil || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		var user auth.User
		ok := false
		if c, err := r.Cookie(sessionCookie); err == nil {
			user, ok = s.Auth.Session(c.Value)
		}
		switch r.URL.Path {
		case "/api/me", "/api/login", "/api/logout":
		default:
			if !ok {
				writeError(w, http.StatusUnauthorized, "sign in required")
				return
			}
		}
		if ok {
			r = r.WithContext(context.WithValue(r.Context(), userKey, user))
		}
		next.ServeHTTP(w, r)
	})
}

func userFrom(r *http.Request) (auth.User, bool) {
	u, ok := r.Context().Value(userKey).(auth.User)
	return u, ok
}

// clientAddr identifies the caller for lockout accounting.
func (s *Server) clientAddr(r *http.Request) string {
	if s.TrustProxy {
		if ip := strings.TrimSpace(r.Header.Get("X-Real-IP")); ip != "" {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type meResponse struct {
	AuthEnabled bool       `json:"authEnabled"`
	User        *auth.User `json:"user"`
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	res := meResponse{AuthEnabled: s.Auth != nil}
	if u, ok := userFrom(r); ok {
		res.User = &u
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.Auth == nil {
		writeError(w, http.StatusBadRequest, "sign-in is not enabled on this server")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	token, user, err := s.Auth.Login(req.Username, req.Password, s.clientAddr(r))
	switch {
	case errors.Is(err, auth.ErrLocked):
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusUnauthorized, auth.ErrInvalid.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: int(auth.SessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, meResponse{AuthEnabled: true, User: &user})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if s.Auth != nil {
		if c, err := r.Cookie(sessionCookie); err == nil {
			s.Auth.Logout(c.Value)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, meResponse{AuthEnabled: s.Auth != nil})
}

// State is the console's live view of the world.
type State struct {
	Info      Info                 `json:"info"`
	Now       time.Time            `json:"now"`
	Clusters  []fleet.Summary      `json:"clusters"`
	Pods      []fleet.PodRow       `json:"pods"`
	Incidents []*incident.Incident `json:"incidents"`
}

func (s *Server) snapshot() State {
	st := State{Info: s.Info, Now: time.Now().UTC(), Clusters: []fleet.Summary{}, Pods: []fleet.PodRow{}, Incidents: s.Store.List()}
	for _, snap := range s.Engine.Snapshots() {
		st.Clusters = append(st.Clusters, snap.Summary)
		st.Pods = append(st.Pods, snap.Pods...)
	}
	return st
}

func (s *Server) state(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.snapshot())
}

func (s *Server) audit(w http.ResponseWriter, _ *http.Request) {
	entries := []incident.Entry{}
	if s.Audit != nil {
		entries = s.Audit.Entries()
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": s.Info.AuditPath, "entries": entries})
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	window := time.Hour
	if d, err := time.ParseDuration(r.URL.Query().Get("window")); err == nil && d > 0 && d <= 24*time.Hour {
		window = d
	}
	writeJSON(w, http.StatusOK, map[string]any{"window": window.String(), "samples": s.Engine.History(time.Now().Add(-window))})
}

// Settings is the read-only configuration view.
type Settings struct {
	Info     Info           `json:"info"`
	Policy   *policy.Policy `json:"policy"`
	Notify   notify.Status  `json:"notify"`
	Auth     bool           `json:"authEnabled"`
	Users    []auth.User    `json:"users,omitempty"`
	Clusters []string       `json:"clusters"`
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	out := Settings{Info: s.Info, Policy: s.Engine.Policy(), Notify: s.Notify.Status(), Auth: s.Auth != nil, Clusters: []string{}}
	for _, snap := range s.Engine.Snapshots() {
		out.Clusters = append(out.Clusters, snap.Summary.Name)
	}
	// Only administrators see who else has an account.
	if u, ok := userFrom(r); s.Auth != nil && ok && u.Role == auth.RoleAdmin {
		out.Users = s.Auth.Users()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) incident(w http.ResponseWriter, r *http.Request) {
	inc, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

type actRequest struct {
	Operator string `json:"operator"`
	Reason   string `json:"reason"`
}

func (s *Server) act(w http.ResponseWriter, r *http.Request) {
	var req actRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// With sign-in enabled the operator is the authenticated user and
	// whatever the request body claims is ignored.
	if s.Auth != nil {
		u, ok := userFrom(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "sign in required")
			return
		}
		if !u.CanApprove() {
			writeError(w, http.StatusForbidden, "your role is viewer; approving, rejecting and rolling back need the approver role")
			return
		}
		req.Operator = u.Name
	}
	id := r.PathValue("id")
	// The write must outlive a browser tab that closes mid-request.
	ctx := context.WithoutCancel(r.Context())

	var inc *incident.Incident
	var err error
	switch r.PathValue("verb") {
	case "approve":
		inc, err = s.Engine.Approve(ctx, id, req.Operator)
	case "reject":
		inc, err = s.Engine.Reject(id, req.Operator, req.Reason)
	case "rollback":
		inc, err = s.Engine.Rollback(ctx, id, req.Operator)
	case "replan":
		inc, err = s.Engine.Replan(ctx, id, req.Operator)
	default:
		writeError(w, http.StatusNotFound, "unknown action")
		return
	}
	if err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, incident.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, incident.ErrWrongState), errors.Is(err, autofix.ErrRefreshed):
		return http.StatusConflict
	case errors.Is(err, autofix.ErrOperatorRequired):
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

// stream is a server-sent-events endpoint. It carries no data, only a
// "change" tick; the console refetches when it fires.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // tell nginx not to buffer the stream
	ch := s.Hub.subscribe()
	defer s.Hub.unsubscribe(ch)

	fmt.Fprint(w, "event: change\ndata: {}\n\n")
	flusher.Flush()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			fmt.Fprint(w, "event: change\ndata: {}\n\n")
		case <-heartbeat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
		}
		flusher.Flush()
		// Coalesce bursts: a rollout produces many changes per second.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
