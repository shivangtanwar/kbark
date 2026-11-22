// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func managerWithFile(t *testing.T) (*Manager, string) {
	t.Helper()
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	m, err := New([]User{
		{Username: "asha", Name: "Asha Rao", Role: RoleApprover, PasswordHash: string(h)},
		{Username: "vik", Role: RoleViewer, PasswordHash: string(h)},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := m.EnableSessionPersistence(path); err != nil {
		t.Fatal(err)
	}
	return m, path
}

// A second Manager over the same users and file stands in for a restart.
func restarted(t *testing.T, path string) *Manager {
	t.Helper()
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	m, err := New([]User{
		{Username: "asha", Name: "Asha Rao", Role: RoleApprover, PasswordHash: string(h)},
		{Username: "vik", Role: RoleViewer, PasswordHash: string(h)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.EnableSessionPersistence(path); err != nil {
		t.Fatal(err)
	}
	return m
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestTokenIsNeverInTheFile(t *testing.T) {
	m, path := managerWithFile(t)
	token, _, err := m.Login("asha", "correct horse battery", "c")
	if err != nil {
		t.Fatal(err)
	}
	body := readFile(t, path)
	if strings.Contains(body, token) {
		t.Fatal("the session token itself is in the file")
	}
	// What is there is the hex SHA-256 of it, with the username and expiry.
	var f persistedSessions
	if err := json.Unmarshal([]byte(body), &f); err != nil {
		t.Fatalf("file is not JSON: %v", err)
	}
	if f.Version != 1 || len(f.Sessions) != 1 || f.Sessions[0].User != "asha" {
		t.Fatalf("unexpected file contents: %s", body)
	}
	if f.Sessions[0].Hash != tokenKey(token) {
		t.Fatalf("stored hash is not the SHA-256 of the token")
	}
	if mode := modeOf(t, path); mode != 0o600 {
		t.Fatalf("session file mode is %o, want 600", mode)
	}
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestRestartRestoresASession(t *testing.T) {
	m, path := managerWithFile(t)
	token, _, err := m.Login("asha", "correct horse battery", "c")
	if err != nil {
		t.Fatal(err)
	}
	m2 := restarted(t, path)
	u, ok := m2.Session(token)
	if !ok || u.Username != "asha" || !u.CanApprove() {
		t.Fatalf("session did not survive the restart: %v %v", u, ok)
	}
}

func TestRestartDropsExpiredAndUnknownSessions(t *testing.T) {
	m, path := managerWithFile(t)
	now := time.Now()
	m.now = func() time.Time { return now }

	fresh, _, err := m.Login("asha", "correct horse battery", "c")
	if err != nil {
		t.Fatal(err)
	}
	stale, _, err := m.Login("vik", "correct horse battery", "c")
	if err != nil {
		t.Fatal(err)
	}
	gone, _, err := m.Login("asha", "correct horse battery", "other")
	if err != nil {
		t.Fatal(err)
	}

	// Expire the "stale" one and the "gone" one in the file directly:
	// rewind the clock, log in, then age the whole file past the TTL.
	m.now = func() time.Time { return now.Add(-2 * SessionTTL) }
	old, _, err := m.Login("vik", "correct horse battery", "c")
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return now }

	// A user who left: write an entry for a username neither manager defines.
	var f persistedSessions
	if err := json.Unmarshal([]byte(readFile(t, path)), &f); err != nil {
		t.Fatal(err)
	}
	f.Sessions = append(f.Sessions, persistedSession{
		Hash: tokenKey("made-up-token"), User: "ghost", Expires: now.Add(time.Hour),
	})
	body, _ := json.Marshal(f)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	m2 := restarted(t, path)
	if _, ok := m2.Session(fresh); !ok {
		t.Error("the live session was dropped")
	}
	if _, ok := m2.Session(old); ok {
		t.Error("the expired session was restored")
	}
	// stale and gone are newer logins of the same users, still valid.
	if _, ok := m2.Session(stale); !ok {
		t.Error("a valid session was dropped")
	}
	if _, ok := m2.Session(gone); !ok {
		t.Error("a valid session was dropped")
	}
	// The unknown user's entry was loaded from the file but must not work.
	if _, ok := m2.Session("made-up-token"); ok {
		t.Error("a session for an unknown user became valid")
	}
	// And the rewrite dropped it from the file.
	if strings.Contains(readFile(t, path), "ghost") {
		t.Error("the unknown user's entry stayed in the file")
	}
}

func TestLogoutRemovesThePersistedSession(t *testing.T) {
	m, path := managerWithFile(t)
	token, _, err := m.Login("asha", "correct horse battery", "c")
	if err != nil {
		t.Fatal(err)
	}
	m.Logout(token)
	m2 := restarted(t, path)
	if _, ok := m2.Session(token); ok {
		t.Fatal("logout did not survive the restart")
	}
	if strings.Contains(readFile(t, path), tokenKey(token)) {
		t.Fatal("the logged-out session is still in the file")
	}
}

func TestCorruptFileSurfacesAnErrorAndContinues(t *testing.T) {
	m, path := managerWithFile(t)
	_ = m
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.MinCost)
	m2, err := New([]User{{Username: "a", Role: RoleViewer, PasswordHash: string(h)}})
	if err != nil {
		t.Fatal(err)
	}
	err = m2.EnableSessionPersistence(path)
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("want an error for a corrupt file, got %v", err)
	}
	// The caller (serve) prints it and carries on: new sessions work
	// and are still persisted.
	token, _, err := m2.Login("a", "x", "c")
	if err != nil {
		t.Fatal(err)
	}
	m3 := restartedUsers(t, path, []User{{Username: "a", Role: RoleViewer, PasswordHash: string(h)}})
	if _, ok := m3.Session(token); !ok {
		t.Fatal("sign-in after a corrupt file was not persisted")
	}
}

func restartedUsers(t *testing.T, path string, users []User) *Manager {
	t.Helper()
	m, err := New(users)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.EnableSessionPersistence(path); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRoleChangeIsPickedUpByARestoredSession(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	m, path := managerWithFile(t)
	token, _, err := m.Login("asha", "correct horse battery", "c")
	if err != nil {
		t.Fatal(err)
	}
	// The users file changes asha from approver to viewer.
	h2, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	m2 := restartedUsers(t, path, []User{
		{Username: "asha", Name: "Asha Rao", Role: RoleViewer, PasswordHash: string(h2)},
		{Username: "vik", Role: RoleViewer, PasswordHash: string(h)},
	})
	u, ok := m2.Session(token)
	if !ok {
		t.Fatal("session was dropped on the role change")
	}
	if u.Role != RoleViewer || u.CanApprove() {
		t.Fatalf("restored session kept the old role: %+v", u)
	}
}

func TestMissingFileIsNotAnError(t *testing.T) {
	h, _ := bcrypt.GenerateFromPassword([]byte("x"), bcrypt.MinCost)
	m, err := New([]User{{Username: "a", Role: RoleViewer, PasswordHash: string(h)}})
	if err != nil {
		t.Fatal(err)
	}
	err = m.EnableSessionPersistence(filepath.Join(t.TempDir(), "nope", "absent.json"))
	if err != nil {
		t.Fatalf("an absent session file must not be an error: %v", err)
	}
}

func TestPersistenceIsOffUntilEnabled(t *testing.T) {
	// A manager that never called EnableSessionPersistence writes
	// nothing: its zero-value sessionPath means memory only.
	h, _ := bcrypt.GenerateFromPassword([]byte("correct horse battery"), bcrypt.MinCost)
	plain, err := New([]User{{Username: "asha", Role: RoleApprover, PasswordHash: string(h)}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(cwd) }()
	token, _, _ := plain.Login("asha", "correct horse battery", "c")
	if _, ok := plain.Session(token); !ok {
		t.Fatal("session not live in memory")
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a manager without a path wrote files: %v", entries)
	}
}
