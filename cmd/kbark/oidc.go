// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/shivangtanwar/kbark/internal/auth"
	"github.com/shivangtanwar/kbark/internal/server"
)

// buildOIDC resolves the users file's optional oidc block into a
// ready provider, or nil when single sign-on is not configured.
func buildOIDC(users *auth.Manager, o serveOptions) (*server.OIDC, error) {
	if users == nil {
		return nil, nil
	}
	cfg := users.OIDC()
	if cfg == nil {
		return nil, nil
	}
	// The provider redirects the browser back to a stable address, so
	// the public URL is required rather than guessed from --addr.
	if o.publicURL == "" {
		return nil, fmt.Errorf("--public-url is required when the users file configures oidc: the provider must redirect back to %s/api/oidc/callback", "<public-url>")
	}
	secret := os.Getenv(cfg.ClientSecretEnv)
	if secret == "" {
		return nil, fmt.Errorf("oidc: client secret not set; put it in the environment variable named by client_secret_env (%s)", cfg.ClientSecretEnv)
	}
	// Discovery runs now, so a wrong issuer fails at startup rather than
	// on the first sign-in attempt.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery at %s: %w", cfg.Issuer, err)
	}
	redirect := o.publicURL + "/api/oidc/callback"
	oauth := oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: secret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  redirect,
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	return server.NewOIDC(cfg, provider, verifier, oauth), nil
}
