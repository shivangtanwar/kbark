// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/shivangtanwar/kbark/internal/auth"
)

func TestBuildOIDCIsNilWithoutConfig(t *testing.T) {
	if sso, err := buildOIDC(nil, serveOptions{}); sso != nil || err != nil {
		t.Fatalf("no users file: %v %v", sso, err)
	}
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	m, err := auth.New([]auth.User{{Username: "asha", Role: auth.RoleApprover, PasswordHash: string(h)}})
	if err != nil {
		t.Fatal(err)
	}
	if sso, err := buildOIDC(m, serveOptions{}); sso != nil || err != nil {
		t.Fatalf("users file without oidc: %v %v", sso, err)
	}
}

// usersFileWithOIDC writes a users file carrying an oidc block and
// returns its path, loaded through auth.Load the way serve does.
func usersFileWithOIDC(t *testing.T, oidcBlock string) *auth.Manager {
	t.Helper()
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	body := "users:\n  - username: asha\n    role: approver\n    password_hash: \"" + string(h) + "\"\n" + oidcBlock
	path := filepath.Join(t.TempDir(), "users.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := auth.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBuildOIDCRequiresAPublicURL(t *testing.T) {
	m := usersFileWithOIDC(t, `oidc:
  issuer: https://idp.example.com
  client_id: kbark
  client_secret_env: S
`)
	_, err := buildOIDC(m, serveOptions{})
	if err == nil || !strings.Contains(err.Error(), "--public-url") {
		t.Fatalf("want a --public-url error, got %v", err)
	}
}

func TestBuildOIDCRequiresTheSecret(t *testing.T) {
	m := usersFileWithOIDC(t, `oidc:
  issuer: https://idp.example.com
  client_id: kbark
  client_secret_env: KBARK_TEST_UNSET_SECRET
`)
	t.Setenv("KBARK_TEST_UNSET_SECRET", "")
	_, err := buildOIDC(m, serveOptions{publicURL: "https://kbark.example.com"})
	if err == nil || !strings.Contains(err.Error(), "client secret") {
		t.Fatalf("want a missing-secret error, got %v", err)
	}
}
