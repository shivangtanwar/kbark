// SPDX-License-Identifier: Apache-2.0

// Package auth gives the dashboard real identities: a users file with
// bcrypt password hashes and roles, and server-side sessions. With it,
// the name recorded against an approval is the signed-in user's, not
// something typed into a text box.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"sigs.k8s.io/yaml"
)

// Roles, in increasing order of power.
const (
	RoleViewer   = "viewer"   // read everything, change nothing
	RoleApprover = "approver" // approve, reject and roll back fixes
	RoleAdmin    = "admin"    // approver, plus settings visibility
)

// SessionTTL is how long a login lasts.
const SessionTTL = 12 * time.Hour

const (
	maxFailures = 5
	lockout     = time.Minute
)

// User is one account in the users file.
type User struct {
	Username     string `json:"username"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	PasswordHash string `json:"password_hash,omitempty"`
}

// CanApprove reports whether the user may change cluster state.
func (u User) CanApprove() bool { return u.Role == RoleApprover || u.Role == RoleAdmin }

// Public strips the hash for sending to the browser.
func (u User) Public() User { u.PasswordHash = ""; return u }

type usersFile struct {
	Users []User `json:"users"`
}

// ErrInvalid is returned for a wrong username or password. The two are
// not distinguished, so the login form does not reveal which usernames exist.
var ErrInvalid = errors.New("invalid username or password")

// ErrLocked is returned while a client is locked out after repeated failures.
var ErrLocked = errors.New("too many failed sign-in attempts; try again in a minute")

type session struct {
	user    User
	expires time.Time
}

type attempts struct {
	count int
	until time.Time
}

// Manager holds users and live sessions. Safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	users    map[string]User
	sessions map[string]session
	failures map[string]*attempts
	now      func() time.Time
}

// Load reads and validates a users file.
func Load(path string) (*Manager, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("users file: %w", err)
	}
	var f usersFile
	if err := yaml.UnmarshalStrict(raw, &f); err != nil {
		return nil, fmt.Errorf("users file %s: %w", path, err)
	}
	return New(f.Users)
}

// New builds a manager from a list of users.
func New(users []User) (*Manager, error) {
	m := &Manager{users: map[string]User{}, sessions: map[string]session{}, failures: map[string]*attempts{}, now: time.Now}
	if len(users) == 0 {
		return nil, errors.New("users file defines no users")
	}
	for _, u := range users {
		u.Username = strings.ToLower(strings.TrimSpace(u.Username))
		switch {
		case u.Username == "":
			return nil, errors.New("users file: a user has no username")
		case u.Role != RoleViewer && u.Role != RoleApprover && u.Role != RoleAdmin:
			return nil, fmt.Errorf("users file: user %q has role %q; want viewer, approver or admin", u.Username, u.Role)
		}
		if _, err := bcrypt.Cost([]byte(u.PasswordHash)); err != nil {
			return nil, fmt.Errorf("users file: user %q has no valid bcrypt password_hash (create one with `kbark users hash`)", u.Username)
		}
		if _, dup := m.users[u.Username]; dup {
			return nil, fmt.Errorf("users file: duplicate user %q", u.Username)
		}
		if u.Name == "" {
			u.Name = u.Username
		}
		m.users[u.Username] = u
	}
	return m, nil
}

// HashPassword returns a bcrypt hash suitable for the users file.
func HashPassword(password string) (string, error) {
	if len(password) < 10 {
		return "", errors.New("password must be at least 10 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(h), err
}

// dummyHash is compared against when the username is unknown, so a
// wrong username takes as long to reject as a wrong password.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("kbark-timing-equaliser"), 12)

// Login checks credentials and returns a new session token. client
// identifies the caller (its address) for lockout accounting.
func (m *Manager) Login(username, password, client string) (string, User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	key := client + "|" + username

	m.mu.Lock()
	now := m.now()
	if a := m.failures[key]; a != nil && now.Before(a.until) {
		m.mu.Unlock()
		return "", User{}, ErrLocked
	}
	u, ok := m.users[username]
	m.mu.Unlock()

	hash := dummyHash
	if ok {
		hash = []byte(u.PasswordHash)
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil || !ok {
		m.mu.Lock()
		a := m.failures[key]
		if a == nil {
			a = &attempts{}
			m.failures[key] = a
		}
		a.count++
		if a.count >= maxFailures {
			a.count, a.until = 0, now.Add(lockout)
		}
		m.mu.Unlock()
		return "", User{}, ErrInvalid
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", User{}, err
	}
	token := hex.EncodeToString(raw)
	m.mu.Lock()
	delete(m.failures, key)
	m.sessions[token] = session{user: u, expires: now.Add(SessionTTL)}
	m.gc(now)
	m.mu.Unlock()
	return token, u.Public(), nil
}

// Session returns the user for a token, if the session is still valid.
func (m *Manager) Session(token string) (User, bool) {
	if token == "" {
		return User{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok || m.now().After(s.expires) {
		delete(m.sessions, token)
		return User{}, false
	}
	return s.user.Public(), true
}

// Logout ends a session.
func (m *Manager) Logout(token string) {
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

// Users lists accounts without hashes, for the settings page.
func (m *Manager) Users() []User {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u.Public())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

// gc drops expired sessions and stale lockout records. Lock held.
func (m *Manager) gc(now time.Time) {
	for t, s := range m.sessions {
		if now.After(s.expires) {
			delete(m.sessions, t)
		}
	}
	for k, a := range m.failures {
		if a.count == 0 && now.After(a.until) {
			delete(m.failures, k)
		}
	}
}
