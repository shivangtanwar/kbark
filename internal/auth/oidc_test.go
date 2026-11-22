// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestOIDCRoleMapping(t *testing.T) {
	c := &OIDCConfig{
		Issuer: "https://idp.example.com", ClientID: "kbark", ClientSecretEnv: "S",
		Roles: map[string]string{
			"asha@example.com": RoleAdmin,
			"*@example.com":    RoleViewer,
			"*@corp.test":      RoleApprover,
		},
	}
	tests := []struct {
		email string
		want  string
		ok    bool
	}{
		{"asha@example.com", RoleAdmin, true}, // exact beats the wildcard
		{"bob@example.com", RoleViewer, true}, // domain wildcard
		{"x@corp.test", RoleApprover, true},   // another domain
		{"mallory@elsewhere.org", "", false},  // no match, no default
		{"ASHA@EXAMPLE.COM", RoleAdmin, true}, // case-insensitive
	}
	for _, tt := range tests {
		got, ok := c.RoleFor(tt.email)
		if ok != tt.ok || got != tt.want {
			t.Errorf("RoleFor(%q) = %q,%v want %q,%v", tt.email, got, ok, tt.want, tt.ok)
		}
	}
}

func TestOIDCDefaultRole(t *testing.T) {
	c := &OIDCConfig{
		Issuer: "https://idp.example.com", ClientID: "kbark", ClientSecretEnv: "S",
		Roles:       map[string]string{"*@example.com": RoleViewer},
		DefaultRole: RoleViewer,
	}
	if role, ok := c.RoleFor("nobody@elsewhere.org"); !ok || role != RoleViewer {
		t.Fatalf("default role = %q,%v", role, ok)
	}
}

func TestOIDCValidate(t *testing.T) {
	base := func() *OIDCConfig {
		return &OIDCConfig{Issuer: "https://idp.example.com", ClientID: "kbark", ClientSecretEnv: "S"}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("a complete block must validate: %v", err)
	}
	for name, mut := range map[string]func(*OIDCConfig){
		"bad issuer":   func(c *OIDCConfig) { c.Issuer = "not a url" },
		"no client id": func(c *OIDCConfig) { c.ClientID = "" },
		"no secret":    func(c *OIDCConfig) { c.ClientSecretEnv = "" },
		"bad role":     func(c *OIDCConfig) { c.Roles = map[string]string{"a@b.com": "root"} },
		"bad default":  func(c *OIDCConfig) { c.DefaultRole = "root" },
		"bad email":    func(c *OIDCConfig) { c.Roles = map[string]string{"not-an-email": RoleViewer} },
	} {
		c := base()
		mut(c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestLoadReadsTheOIDCBlock(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	body := `users:
  - username: asha
    role: approver
    password_hash: "` + string(h) + `"
oidc:
  issuer: https://accounts.example.com
  client_id: kbark
  client_secret_env: KBARK_OIDC_CLIENT_SECRET
  name: "Company SSO"
  roles:
    asha@example.com: admin
    "*@example.com": viewer
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := m.OIDC()
	if cfg == nil || cfg.Issuer != "https://accounts.example.com" || cfg.Label() != "Company SSO" {
		t.Fatalf("oidc block not loaded: %+v", cfg)
	}
	// The resolver the manager got maps SSO emails to roles.
	if u, ok := m.sso("asha@example.com"); !ok || u.Role != RoleAdmin {
		t.Fatalf("sso user: %+v %v", u, ok)
	}
}

func TestLoadRejectsABadOIDCBlock(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	body := `users:
  - username: asha
    role: approver
    password_hash: "` + string(h) + `"
oidc:
  issuer: https://accounts.example.com
  client_id: kbark
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a users file with an incomplete oidc block must be refused")
	}
}

func TestNoOIDCBlockMeansNoSSO(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	m, err := New([]User{{Username: "asha", Role: RoleApprover, PasswordHash: string(h)}})
	if err != nil {
		t.Fatal(err)
	}
	if m.OIDC() != nil {
		t.Fatal("a manager built with New must have no OIDC config")
	}
	if m.sso != nil {
		if _, ok := m.sso("asha@example.com"); ok {
			t.Fatal("no resolver means no SSO user")
		}
	}
}
