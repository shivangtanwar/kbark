// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

// OIDCConfig is the optional single-sign-on block of the users file:
//
//	oidc:
//	  issuer: https://accounts.example.com
//	  client_id: kbark
//	  client_secret_env: KBARK_OIDC_CLIENT_SECRET
//	  name: "Company SSO"
//	  roles:
//	    asha@example.com: admin
//	    "*@example.com": viewer
//	  default_role: ""
//
// The secret is never in the file; only the name of the env var holding
// it. Roles map the verified email to a kbark role: an exact match, or
// a leading "*@" wildcard that matches a whole domain. Anyone not
// matched is refused unless default_role is set.
type OIDCConfig struct {
	Issuer          string            `json:"issuer"`
	ClientID        string            `json:"client_id"`
	ClientSecretEnv string            `json:"client_secret_env"`
	Name            string            `json:"name,omitempty"`
	Roles           map[string]string `json:"roles,omitempty"`
	DefaultRole     string            `json:"default_role,omitempty"`
}

// Label returns the button text, with a usable default.
func (c *OIDCConfig) Label() string {
	if c.Name == "" {
		return "single sign-on"
	}
	return c.Name
}

// Validate checks the block is complete enough to use.
func (c *OIDCConfig) Validate() error {
	if !strings.HasPrefix(c.Issuer, "https://") && !strings.HasPrefix(c.Issuer, "http://") {
		return fmt.Errorf("users file: oidc.issuer %q is not an http(s) URL", c.Issuer)
	}
	if c.ClientID == "" {
		return errors.New("users file: oidc.client_id is missing")
	}
	if c.ClientSecretEnv == "" {
		return errors.New("users file: oidc.client_secret_env is missing")
	}
	for email, role := range c.Roles {
		if !strings.Contains(email, "*@") && !isEmail(email) {
			return fmt.Errorf("users file: oidc role key %q is not an email address", email)
		}
		if err := validRole(role); err != nil {
			return fmt.Errorf("users file: oidc role for %q: %w", email, err)
		}
	}
	if err := validRole(c.DefaultRole); err != nil {
		return fmt.Errorf("users file: oidc.default_role: %w", err)
	}
	return nil
}

func validRole(role string) error {
	switch role {
	case "", RoleViewer, RoleApprover, RoleAdmin:
		return nil
	}
	return fmt.Errorf("role %q is not viewer, approver or admin", role)
}

// isEmail is a deliberately light check: exact keys must merely look
// like an address with one @, since providers validate the real thing.
func isEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// RoleFor maps a verified email to a kbark role. An exact match wins
// over a domain wildcard; several wildcards must agree, or the email is
// refused rather than guessed. ok is false when nobody is mapped and
// there is no default.
func (c *OIDCConfig) RoleFor(email string) (role string, ok bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	if r, found := c.Roles[email]; found {
		return r, true
	}
	if strings.Contains(email, "@") {
		if r, found := c.Roles["*@"+domain(email)]; found {
			return r, true
		}
	}
	if c.DefaultRole != "" {
		return c.DefaultRole, true
	}
	return "", false
}

func domain(email string) string {
	_, dom, _ := strings.Cut(email, "@")
	return dom
}

// ssoUser synthesises the account a single-sign-on session belongs to.
// The username is the email: it is the only stable identifier the
// provider guarantees, and it is what the audit log should show.
func (c *OIDCConfig) ssoUser(email string) (User, bool) {
	role, ok := c.RoleFor(email)
	if !ok {
		return User{}, false
	}
	return User{Username: strings.ToLower(strings.TrimSpace(email)), Name: email, Role: role}, true
}

// Resolver returns the lookup for Manager.SetSSOResolver.
func (c *OIDCConfig) Resolver() func(username string) (User, bool) {
	return func(username string) (User, bool) { return c.ssoUser(username) }
}
