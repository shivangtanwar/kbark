// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"

	"github.com/shivangtanwar/kbark/internal/auth"
)

// fakeIDP is a minimal OpenID Connect provider: discovery, JWKS and a
// token endpoint. It signs RS256 ID tokens with a key generated for
// the test, so the real go-oidc verification path runs end to end.
type fakeIDP struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string

	mu           sync.Mutex
	codes        map[string]string // authorization code -> email it vouches for
	claims       map[string]func() map[string]any
	exchanges    int
	lastVerifier string
}

func newFakeIDP(t *testing.T, clientID string) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIDP{key: key, clientID: clientID, codes: map[string]string{}, claims: map[string]func() map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                                p.issuer(),
			"authorization_endpoint":                p.issuer() + "/authorize",
			"token_endpoint":                        p.issuer() + "/token",
			"jwks_uri":                              p.issuer() + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"keys": []any{p.jwk()}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.exchanges++
		p.lastVerifier = r.PostFormValue("code_verifier")
		code := r.PostFormValue("code")
		email, ok := p.codes[code]
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		// The fake does not render an authorization page; whoever drives
		// the flow registers the code directly.
		extra := map[string]any{}
		if fn := p.claims[code]; fn != nil {
			extra = fn()
		}
		id, err := p.signIDToken(email, extra)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": "at-" + code,
			"token_type":   "Bearer",
			"id_token":     id,
		})
	})
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeIDP) issuer() string { return p.srv.URL }

// register hands out an authorization code for email, with optional
// claim overrides (nonce, email_verified...).
func (p *fakeIDP) register(email string, extra func() map[string]any) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	code := fmt.Sprintf("code-%d", len(p.codes))
	p.codes[code] = email
	p.claims[code] = extra
	return code
}

func (p *fakeIDP) verifierSent() (string, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastVerifier, p.exchanges
}

// signIDToken builds and signs a compact RS256 JWT.
func (p *fakeIDP) signIDToken(email string, extra map[string]any) (string, error) {
	claims := map[string]any{
		"iss":   p.issuer(),
		"aud":   p.clientID,
		"exp":   float64(nowUnix() + 3600),
		"iat":   float64(nowUnix()),
		"sub":   email,
		"email": email,
	}
	for k, v := range extra {
		claims[k] = v
	}
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": "test-key"}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	// RS256 signs the SHA-256 digest of the signing input.
	digest := sha256.Sum256([]byte(signing))
	sum, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sum), nil
}

// jwk publishes the public key.
func (p *fakeIDP) jwk() map[string]any {
	return map[string]any{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test-key",
		"n": base64.RawURLEncoding.EncodeToString(p.key.PublicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.key.PublicKey.E)).Bytes()),
	}
}

func nowUnix() int64 { return time.Now().Unix() }

// oidcTestServer builds a console server with SSO wired to the fake.
func oidcTestServer(t *testing.T, idp *fakeIDP, roles map[string]string, defaultRole string) (http.Handler, *oidcFlows) {
	t.Helper()
	h, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	m, err := auth.New([]auth.User{{Username: "asha", Name: "Asha Rao", Role: auth.RoleApprover, PasswordHash: string(h)}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &auth.OIDCConfig{
		Issuer: idp.issuer(), ClientID: idp.clientID, ClientSecretEnv: "KBARK_TEST_SECRET", Name: "Acme SSO",
		Roles: roles, DefaultRole: defaultRole,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	m.SetSSOResolver(cfg.Resolver())
	provider, err := oidc.NewProvider(context.Background(), idp.issuer())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("KBARK_TEST_SECRET", "s3cret")
	flows := &oidcFlows{}
	s := &Server{
		Auth: m, OIDC: &OIDC{
			Config: cfg, Provider: provider, Verifier: provider.Verifier(&oidc.Config{ClientID: idp.clientID}),
			OAuth: oauth2.Config{
				ClientID: idp.clientID, ClientSecret: "s3cret", Endpoint: provider.Endpoint(),
				RedirectURL: "http://console.example/api/oidc/callback",
				Scopes:      []string{oidc.ScopeOpenID, "email"},
			},
			flows: flows,
		},
	}
	return s.Handler(), flows
}

// driveLogin performs the client's half of the handshake: hit the login
// route, then "approve" at the provider (the fake needs no page: the
// test registers a code), then hit the callback with state and code.
func driveLogin(t *testing.T, h http.Handler, idp *fakeIDP, email string, extra func() map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	rec := do(h, "GET", "/api/oidc/login", host, nil, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("login start = %d %s", rec.Code, rec.Body)
	}
	loc := rec.Header().Get("Location")
	state := queryParam(t, loc, "state")
	nonce := queryParam(t, loc, "nonce")
	challenge := queryParam(t, loc, "code_challenge")
	if state == "" || nonce == "" || challenge == "" || queryParam(t, loc, "code_challenge_method") != "S256" {
		t.Fatalf("authorization URL is missing state/nonce/PKCE: %s", loc)
	}
	code := idp.register(email, func() map[string]any {
		cl := map[string]any{"nonce": nonce}
		if extra != nil {
			for k, v := range extra() {
				cl[k] = v
			}
		}
		return cl
	})
	cb := do(h, "GET", "/api/oidc/callback?state="+state+"&code="+code, host, nil, "")
	return cb
}

func queryParam(t *testing.T, rawurl, name string) string {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatalf("authorization URL %q: %v", rawurl, err)
	}
	return u.Query().Get(name)
}

func TestOIDCHappyPathCreatesASession(t *testing.T) {
	idp := newFakeIDP(t, "kbark")
	h, flows := oidcTestServer(t, idp, map[string]string{
		"asha@example.com": auth.RoleAdmin,
		"*@example.com":    auth.RoleViewer,
	}, "")

	rec := driveLogin(t, h, idp, "asha@example.com", nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback = %d %s (location %q)", rec.Code, rec.Body, rec.Header().Get("Location"))
	}
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("callback did not set a session cookie")
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie must be HttpOnly and SameSite=Strict: %+v", session)
	}
	// The session behaves like a password login's: it identifies a user
	// with the mapped role.
	me := do(h, "GET", "/api/me", host, nil, "", session)
	var out meResponse
	if err := json.Unmarshal(me.Body.Bytes(), &out); err != nil || me.Code != http.StatusOK {
		t.Fatalf("/api/me: %d %v %s", me.Code, err, me.Body)
	}
	if out.User == nil || out.User.Username != "asha@example.com" || out.User.Role != auth.RoleAdmin {
		t.Fatalf("SSO user: %+v", out.User)
	}
	if out.SSO == nil || out.SSO.Name != "Acme SSO" {
		t.Fatalf("/api/me must advertise the SSO button: %+v", out.SSO)
	}
	_ = flows
}

func TestOIDCDomainWildcardMatch(t *testing.T) {
	idp := newFakeIDP(t, "kbark")
	h, _ := oidcTestServer(t, idp, map[string]string{
		"asha@example.com": auth.RoleAdmin,
		"*@example.com":    auth.RoleViewer,
	}, "")
	rec := driveLogin(t, h, idp, "newbie@example.com", nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("wildcard sign-in = %d %s", rec.Code, rec.Body)
	}
}

func TestOIDCCookieIsSetOnTheCallbackNotRequiredByIt(t *testing.T) {
	// The session cookie is SameSite=Strict. That is safe here because
	// the browser never needs to *send* it on the cross-site hop: the
	// provider redirects to /api/oidc/callback, which sets the cookie on
	// its 302 response; the next request (the SPA loading at "/") is
	// same-site, so Strict sends it. The callback itself must therefore
	// work with no cookie at all.
	idp := newFakeIDP(t, "kbark")
	h, _ := oidcTestServer(t, idp, map[string]string{"*@example.com": auth.RoleViewer}, "")
	rec := driveLogin(t, h, idp, "asha@example.com", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("callback = %d, want a redirect", rec.Code)
	}
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("callback must set an HttpOnly Strict cookie: %+v", session)
	}
	// The follow-up same-site request carries it and is authenticated.
	if me := do(h, "GET", "/api/me", host, nil, "", session); me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"username":"asha@example.com"`) {
		t.Fatalf("the cookie set by the callback did not authenticate the next request: %d %s", me.Code, me.Body)
	}
}

func TestOIDCUnknownEmailIsRefused(t *testing.T) {
	idp := newFakeIDP(t, "kbark")
	h, _ := oidcTestServer(t, idp, map[string]string{"*@example.com": auth.RoleViewer}, "")
	rec := driveLogin(t, h, idp, "mallory@elsewhere.org", nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?sso_error=denied" {
		t.Fatalf("unknown email = %d %s (location %q)", rec.Code, rec.Body, rec.Header().Get("Location"))
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("a refused sign-in must not set a session cookie")
	}
}

func TestOIDCUnverifiedEmailIsRefused(t *testing.T) {
	idp := newFakeIDP(t, "kbark")
	h, _ := oidcTestServer(t, idp, map[string]string{"*@example.com": auth.RoleViewer}, "")
	rec := driveLogin(t, h, idp, "asha@example.com", func() map[string]any {
		return map[string]any{"email_verified": false}
	})
	if rec.Header().Get("Location") != "/?sso_error=denied" {
		t.Fatalf("unverified email = %q, want ?sso_error=denied", rec.Header().Get("Location"))
	}
}

func TestOIDCWrongStateIsRefused(t *testing.T) {
	idp := newFakeIDP(t, "kbark")
	h, _ := oidcTestServer(t, idp, map[string]string{"*@example.com": auth.RoleViewer}, "")
	// A completed handshake: start it by hand so the state is known.
	start := do(h, "GET", "/api/oidc/login", host, nil, "")
	if start.Code != http.StatusFound {
		t.Fatalf("login start = %d", start.Code)
	}
	loc := start.Header().Get("Location")
	state := queryParam(t, loc, "state")
	nonce := queryParam(t, loc, "nonce")
	code := idp.register("asha@example.com", func() map[string]any { return map[string]any{"nonce": nonce} })
	rec := do(h, "GET", "/api/oidc/callback?state="+state+"&code="+code, host, nil, "")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("first sign-in = %d %s", rec.Code, rec.Body)
	}
	// The same state and code, replayed: the state was consumed, so the
	// replay must not reach the token endpoint at all.
	rec = do(h, "GET", "/api/oidc/callback?state="+state+"&code="+code, host, nil, "")
	if rec.Header().Get("Location") != "/?sso_error=state" {
		t.Fatalf("replayed state = %q, want ?sso_error=state", rec.Header().Get("Location"))
	}
	// A forged state with a stolen code fails the same way.
	rec = do(h, "GET", "/api/oidc/callback?state=forged&code="+code, host, nil, "")
	if rec.Header().Get("Location") != "/?sso_error=state" {
		t.Fatalf("forged state = %q, want ?sso_error=state", rec.Header().Get("Location"))
	}
}

func TestOIDCWrongNonceIsRefused(t *testing.T) {
	idp := newFakeIDP(t, "kbark")
	h, _ := oidcTestServer(t, idp, map[string]string{"*@example.com": auth.RoleViewer}, "")

	rec := do(h, "GET", "/api/oidc/login", host, nil, "")
	state := queryParam(t, rec.Header().Get("Location"), "state")
	code := idp.register("asha@example.com", func() map[string]any {
		return map[string]any{"nonce": "someone-elses-nonce"}
	})
	rec = do(h, "GET", "/api/oidc/callback?state="+state+"&code="+code, host, nil, "")
	if rec.Header().Get("Location") != "/?sso_error=token" {
		t.Fatalf("wrong nonce = %q, want ?sso_error=token", rec.Header().Get("Location"))
	}
}

func TestOIDCProviderErrorRedirectsToLogin(t *testing.T) {
	idp := newFakeIDP(t, "kbark")
	h, _ := oidcTestServer(t, idp, map[string]string{"*@example.com": auth.RoleViewer}, "")
	rec := do(h, "GET", "/api/oidc/callback?state=unknown&error=access_denied&error_description=<script>alert(1)</script>", host, nil, "")
	if rec.Header().Get("Location") != "/?sso_error=state" {
		t.Fatalf("provider error = %q", rec.Header().Get("Location"))
	}
	// The provider's description must not be reflected.
	if strings.Contains(rec.Header().Get("Location"), "alert") {
		t.Fatal("provider-supplied text reached the redirect")
	}
}

func TestOIDCRoutesWorkWithoutASession(t *testing.T) {
	// The two OIDC routes must be reachable without a session, or the
	// flow could never start.
	idp := newFakeIDP(t, "kbark")
	h, _ := oidcTestServer(t, idp, map[string]string{"*@example.com": auth.RoleViewer}, "")
	for _, path := range []string{"/api/oidc/login", "/api/oidc/callback?state=x&code=y"} {
		if rec := do(h, "GET", path, host, nil, ""); rec.Code == http.StatusUnauthorized {
			t.Errorf("GET %s without a session = 401", path)
		}
	}
	// Everything else still needs one.
	if rec := do(h, "GET", "/api/state", host, nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/state without a session = %d, want 401", rec.Code)
	}
}

func TestOIDCPKCEVerifierIsSent(t *testing.T) {
	idp := newFakeIDP(t, "kbark")
	h, _ := oidcTestServer(t, idp, map[string]string{"*@example.com": auth.RoleViewer}, "")
	rec := do(h, "GET", "/api/oidc/login", host, nil, "")
	state := queryParam(t, rec.Header().Get("Location"), "state")
	code := idp.register("asha@example.com", nil)
	// No nonce claim: the fake adds one from the query; the driveLogin
	// helper passes it. Here the handshake is done by hand without the
	// nonce echo, so give the token the login's nonce directly.
	nonce := queryParam(t, rec.Header().Get("Location"), "nonce")
	idp.claims[code] = func() map[string]any { return map[string]any{"nonce": nonce} }
	do(h, "GET", "/api/oidc/callback?state="+state+"&code="+code, host, nil, "")
	verifier, exchanges := idp.verifierSent()
	if exchanges != 1 {
		t.Fatalf("exchanges = %d, want 1", exchanges)
	}
	if verifier == "" {
		t.Fatal("no PKCE code_verifier was sent with the token exchange")
	}
	// It is the verifier, not the challenge, and it is long enough to be real.
	if len(verifier) < 43 {
		t.Fatalf("PKCE verifier too short: %q", verifier)
	}
}

func TestOIDCRoutesAreAbsentWithoutConfig(t *testing.T) {
	h := newServer(t, true).Handler()
	if rec := do(h, "GET", "/api/oidc/login", host, nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("login without config = %d, want 404", rec.Code)
	}
	if rec := do(h, "GET", "/api/oidc/callback", host, nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("callback without config = %d, want 404", rec.Code)
	}
}

// The login page needs the label; the wire test pins that /api/me only
// advertises SSO when it is configured.
func TestMeWithoutSSOHasNoLabel(t *testing.T) {
	h := newServer(t, true).Handler()
	var out meResponse
	rec := do(h, "GET", "/api/me", host, nil, "")
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.SSO != nil {
		t.Fatalf("/api/me advertised SSO without a provider: %+v", out.SSO)
	}
}
