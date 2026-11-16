// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func manager(t *testing.T) *Manager {
	t.Helper()
	// Low cost keeps the test fast; production hashes use cost 12.
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	m, err := New([]User{
		{Username: "Asha", Name: "Asha Rao", Role: RoleApprover, PasswordHash: string(h)},
		{Username: "vik", Role: RoleViewer, PasswordHash: string(h)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLoginSessionLogout(t *testing.T) {
	m := manager(t)
	token, u, err := m.Login(" ASHA ", "correct horse battery", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Asha Rao" || !u.CanApprove() || u.PasswordHash != "" {
		t.Fatalf("unexpected user: %+v", u)
	}
	if got, ok := m.Session(token); !ok || got.Username != "asha" {
		t.Fatalf("session not found: %+v %v", got, ok)
	}
	m.Logout(token)
	if _, ok := m.Session(token); ok {
		t.Fatal("session survived logout")
	}
	if _, ok := m.Session(""); ok {
		t.Fatal("empty token must not be a session")
	}
}

func TestViewerCannotApprove(t *testing.T) {
	m := manager(t)
	_, u, err := m.Login("vik", "correct horse battery", "c")
	if err != nil || u.CanApprove() || u.Name != "vik" {
		t.Fatalf("viewer: %+v %v", u, err)
	}
}

func TestWrongCredentialsLookTheSame(t *testing.T) {
	m := manager(t)
	_, _, e1 := m.Login("asha", "wrong", "c")
	_, _, e2 := m.Login("nobody", "wrong", "c")
	if !errors.Is(e1, ErrInvalid) || !errors.Is(e2, ErrInvalid) {
		t.Fatalf("want ErrInvalid for both, got %v and %v", e1, e2)
	}
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	m := manager(t)
	now := time.Now()
	m.now = func() time.Time { return now }
	for i := 0; i < maxFailures; i++ {
		_, _, _ = m.Login("asha", "wrong", "c")
	}
	if _, _, err := m.Login("asha", "correct horse battery", "c"); !errors.Is(err, ErrLocked) {
		t.Fatalf("want lockout even with the right password, got %v", err)
	}
	if _, _, err := m.Login("asha", "correct horse battery", "other-client"); err != nil {
		t.Fatalf("lockout must be per client: %v", err)
	}
	now = now.Add(lockout + time.Second)
	if _, _, err := m.Login("asha", "correct horse battery", "c"); err != nil {
		t.Fatalf("lockout should expire: %v", err)
	}
}

func TestSessionExpires(t *testing.T) {
	m := manager(t)
	now := time.Now()
	m.now = func() time.Time { return now }
	token, _, _ := m.Login("asha", "correct horse battery", "c")
	now = now.Add(SessionTTL + time.Minute)
	if _, ok := m.Session(token); ok {
		t.Fatal("session should have expired")
	}
}

func TestUsersFileValidation(t *testing.T) {
	good, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.MinCost)
	for name, users := range map[string][]User{
		"none":        nil,
		"bad role":    {{Username: "a", Role: "root", PasswordHash: string(good)}},
		"plain text":  {{Username: "a", Role: RoleAdmin, PasswordHash: "hunter2"}},
		"duplicate":   {{Username: "a", Role: RoleAdmin, PasswordHash: string(good)}, {Username: "A", Role: RoleViewer, PasswordHash: string(good)}},
		"no username": {{Role: RoleAdmin, PasswordHash: string(good)}},
	} {
		if _, err := New(users); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := HashPassword("short"); err == nil {
		t.Error("short passwords must be refused")
	}
}
