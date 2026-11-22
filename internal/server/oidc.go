// SPDX-License-Identifier: Apache-2.0

// OpenID Connect single sign-on: an authorization-code flow with PKCE
// that ends in a normal kbark session, so every existing protection
// (role checks, audit names, the SameSite=Strict cookie) applies
// unchanged.
//
// Login state (the OAuth state parameter, the nonce, the PKCE verifier)
// is kept server-side in a short-lived in-memory table, not in a
// cookie: nothing about the handshake is then visible to the browser,
// and a leaked or forged cookie cannot drive a flow. The trade-off is
// that two `kbark serve` processes behind one address share nothing —
// the console is a single process, so that costs nothing here.

package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/shivangtanwar/kbark/internal/auth"
)

// oidcStateTTL bounds how long a started sign-in may take.
const oidcStateTTL = 5 * time.Minute

// ssoErrorText maps the short codes sent to /?sso_error=<code> to the
// plain sentence the login page shows. The codes and sentences are
// kbark's own words: an error string from a provider is untrusted text
// and never reaches the page.
var ssoErrorText = map[string]string{
	"state":    "The sign-in request expired or was tampered with. Start again.",
	"provider": "The sign-in provider refused the sign-in. Start again.",
	"token":    "The sign-in provider did not respond as expected. Start again.",
	"denied":   "This account is not allowed to sign in to kbark.",
}

// OIDC is the configured provider, wired at startup by
// cmd/kbark/serve.go. It is nil when single sign-on is off.
type OIDC struct {
	Config   *auth.OIDCConfig
	Provider *oidc.Provider
	Verifier *oidc.IDTokenVerifier
	OAuth    oauth2.Config
	flows    *oidcFlows
}

// NewOIDC builds a provider wrapper with its in-flight flow table ready.
func NewOIDC(cfg *auth.OIDCConfig, p *oidc.Provider, v *oidc.IDTokenVerifier, o oauth2.Config) *OIDC {
	return &OIDC{Config: cfg, Provider: p, Verifier: v, OAuth: o, flows: &oidcFlows{}}
}

// oidcFlow is one started sign-in.
type oidcFlow struct {
	verifier string // PKCE code_verifier
	nonce    string
	expires  time.Time
}

// oidcFlows tracks in-flight sign-ins.
type oidcFlows struct {
	mu    sync.Mutex
	flows map[string]*oidcFlow
}

func (f *oidcFlows) put(key string, fl *oidcFlow) {
	f.mu.Lock()
	if f.flows == nil {
		f.flows = map[string]*oidcFlow{}
	}
	f.flows[key] = fl
	now := time.Now()
	for k, v := range f.flows {
		if now.After(v.expires) {
			delete(f.flows, k)
		}
	}
	f.mu.Unlock()
}

// take removes and returns the flow for key. A state value is
// single-use: a replayed callback must fail.
func (f *oidcFlows) take(key string) (*oidcFlow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.flows[key]
	if !ok || time.Now().After(fl.expires) {
		delete(f.flows, key)
		return nil, false
	}
	delete(f.flows, key)
	return fl, true
}

// AuthCodeURL builds the provider's authorization URL with the PKCE
// challenge for this flow's verifier.
func (o *OIDC) authCodeURL(state, nonce, verifier string) string {
	return o.OAuth.AuthCodeURL(
		state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("code_challenge", pkceChallenge(verifier)),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

func randomToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		// crypto/rand does not fail on the platforms kbark targets, and
		// carrying on with a predictable token would be worse.
		panic(err)
	}
	return hex.EncodeToString(raw)
}

// pkceChallenge derives the S256 challenge from a verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// oidcLogin starts the handshake: state, nonce and PKCE verifier are
// created and kept server-side, then the browser is pointed at the
// provider. The route is reachable without a session, like /api/login.
func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	o := s.OIDC
	if o == nil {
		writeError(w, http.StatusNotFound, "single sign-on is not enabled on this server")
		return
	}
	state, nonce, verifier := randomToken(), randomToken(), randomToken()
	o.flows.put(state, &oidcFlow{verifier: verifier, nonce: nonce, expires: time.Now().Add(oidcStateTTL)})
	http.Redirect(w, r, o.authCodeURL(state, nonce, verifier), http.StatusFound)
}

// oidcCallback finishes the handshake: verify the state, exchange the
// code (presenting the PKCE verifier), verify the ID token's
// signature, issuer, audience, expiry and nonce, then map the verified
// email to a role and create a session exactly like password login.
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	o := s.OIDC
	if o == nil {
		writeError(w, http.StatusNotFound, "single sign-on is not enabled on this server")
		return
	}
	fl, ok := o.flows.take(r.URL.Query().Get("state"))
	if !ok {
		s.ssoError(w, r, "state")
		return
	}
	// The provider refused (e.g. the user backed out of its consent
	// page). Its error description is untrusted text; use our code.
	if r.URL.Query().Get("error") != "" {
		s.ssoError(w, r, "provider")
		return
	}
	ctx := r.Context()
	tok, err := o.OAuth.Exchange(
		ctx, r.URL.Query().Get("code"),
		oauth2.SetAuthURLParam("code_verifier", fl.verifier),
	)
	if err != nil {
		s.ssoError(w, r, "token")
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		s.ssoError(w, r, "token")
		return
	}
	idt, err := o.Verifier.Verify(ctx, rawID)
	if err != nil {
		s.ssoError(w, r, "token")
		return
	}
	var claims struct {
		Nonce         string `json:"nonce"`
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
	}
	if err := idt.Claims(&claims); err != nil {
		s.ssoError(w, r, "token")
		return
	}
	// The nonce ties this ID token to this handshake, defeating a code
	// injected from another tab or site.
	if claims.Nonce == "" || claims.Nonce != fl.nonce {
		s.ssoError(w, r, "token")
		return
	}
	// email_verified must be true when the claim is present. Providers
	// that omit it entirely are accepted; the role mapping is the gate.
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		s.ssoError(w, r, "denied")
		return
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" || !strings.Contains(email, "@") {
		s.ssoError(w, r, "denied")
		return
	}
	if s.Auth == nil {
		// OIDC is only built when accounts exist; this keeps a
		// misconfiguration a clean error rather than a panic.
		s.ssoError(w, r, "denied")
		return
	}
	token, _, err := s.Auth.LoginSSO(email)
	if err != nil {
		// No role mapping for this address and no default role.
		s.ssoError(w, r, "denied")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: int(auth.SessionTTL.Seconds()),
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// ssoError sends the browser to the login page with a short code. The
// page turns the code into a fixed sentence; no provider-supplied text
// is ever put in a URL or rendered.
func (s *Server) ssoError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?sso_error="+code, http.StatusFound)
}
