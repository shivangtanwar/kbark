// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Sessions can survive a restart. Only a SHA-256 hash of each token is
// written to disk — never the token itself — so a leaked sessions file
// cannot be replayed against the server. Entries whose user is gone or
// whose expiry has passed are dropped on load.

// tokenKey is the map key for a session token. Keying sessions (and the
// persisted file) by this hash means neither memory nor disk ever holds
// the bearer token itself.
func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// persistedSessions is the on-disk form. Version 1.
type persistedSessions struct {
	Version  int                `json:"version"`
	Sessions []persistedSession `json:"sessions"`
}

type persistedSession struct {
	Hash    string    `json:"hash"` // SHA-256 of the token, hex
	User    string    `json:"user"` // username
	Expires time.Time `json:"expires"`
}

// EnableSessionPersistence stores sessions in path and loads whatever
// it holds: expired entries and entries for users that no longer exist
// are dropped. A missing file is fine; an unreadable one is an error,
// and the caller decides whether to carry on (sessions then start
// empty, and new ones are still persisted).
func (m *Manager) EnableSessionPersistence(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionPath = path
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("session file: %w", err)
	}
	var f persistedSessions
	if err := json.Unmarshal(body, &f); err != nil {
		return fmt.Errorf("session file %s is not readable (sessions were not restored): %w", path, err)
	}
	now := m.now()
	for _, s := range f.Sessions {
		if s.Hash == "" || s.User == "" || now.After(s.Expires) {
			continue
		}
		u, ok := m.userFor(s.User)
		if !ok {
			continue
		}
		m.sessions[s.Hash] = session{user: u, expires: s.Expires}
	}
	// Rewrite so dropped entries (expired, removed users) are gone from
	// the file too, and a restart does not warn about them again.
	m.persistLocked()
	return nil
}

// SetSSOResolver tells the manager how to resolve an account that is
// not in the users file: single sign-on maps emails to roles instead.
// Call before EnableSessionPersistence so restored SSO sessions pick
// up the current mapping.
func (m *Manager) SetSSOResolver(fn func(username string) (User, bool)) {
	m.mu.Lock()
	m.sso = fn
	m.mu.Unlock()
}

// LoginSSO creates a session for a user the sign-on provider vouched
// for, exactly like Login does after a password check. The username
// must resolve through the users file or the SSO mapping.
func (m *Manager) LoginSSO(username string) (string, User, error) {
	u, ok := m.userFor(username)
	if !ok {
		return "", User{}, ErrInvalid
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", User{}, err
	}
	token := hex.EncodeToString(raw)
	now := m.now()
	m.mu.Lock()
	m.sessions[tokenKey(token)] = session{user: u, expires: now.Add(SessionTTL)}
	m.gc(now)
	m.persistLocked()
	m.mu.Unlock()
	return token, u.Public(), nil
}

// userFor resolves a username to the account as it stands now, so a
// restored session picks up a changed role (or is dropped when the
// account is gone). Lock held or single-threaded.
func (m *Manager) userFor(username string) (User, bool) {
	if u, ok := m.users[username]; ok {
		return u, true
	}
	if m.sso != nil {
		return m.sso(username)
	}
	return User{}, false
}

// persistLocked writes the session table atomically. Lock held. Write
// failures are swallowed on purpose: a session that cannot be written
// still works for this process, and refusing a sign-in over it would
// trade a convenience for an outage.
func (m *Manager) persistLocked() {
	if m.sessionPath == "" {
		return
	}
	f := persistedSessions{Version: 1}
	for k, s := range m.sessions {
		f.Sessions = append(f.Sessions, persistedSession{Hash: k, User: s.user.Username, Expires: s.expires})
	}
	// Sorted, so two writes of the same table produce the same bytes.
	sort.Slice(f.Sessions, func(i, j int) bool { return f.Sessions[i].Hash < f.Sessions[j].Hash })
	body, err := json.Marshal(f)
	if err != nil {
		return
	}
	dir := filepath.Dir(m.sessionPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp := m.sessionPath + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, m.sessionPath)
}
