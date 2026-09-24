// Package secret keeps passwords and key passphrases out of the JSON store.
// "Remembered" secrets go to the OS keychain (Windows Credential Manager,
// macOS Keychain, Secret Service on Linux); everything else lives only in
// memory for the current session.
package secret

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

var ErrNotFound = errors.New("secret not found")

const service = "termward"

func HostPassword(hostID string) string { return "host/" + hostID + "/password" }
func KeyPassphrase(keyID string) string { return "key/" + keyID + "/passphrase" }

type Store struct {
	mu      sync.RWMutex
	session map[string]string
	// persistent is swappable so tests never touch the real OS keychain.
	persistent Backend
}

type Backend interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
}

func New() *Store { return &Store{session: map[string]string{}, persistent: osKeyring{}} }

func NewWithBackend(b Backend) *Store { return &Store{session: map[string]string{}, persistent: b} }

func (s *Store) Get(name string) (string, error) {
	s.mu.RLock()
	v, ok := s.session[name]
	s.mu.RUnlock()
	if ok {
		return v, nil
	}
	v, err := s.persistent.Get(name)
	if err != nil {
		return "", ErrNotFound
	}
	s.mu.Lock()
	s.session[name] = v
	s.mu.Unlock()
	return v, nil
}

// Put stores a secret for this session and, when remember is true, in the OS
// keychain. A keychain failure is returned but the session copy is kept so the
// current connection still works.
func (s *Store) Put(name, value string, remember bool) error {
	s.mu.Lock()
	s.session[name] = value
	s.mu.Unlock()
	if remember {
		return s.persistent.Set(name, value)
	}
	return nil
}

func (s *Store) Forget(name string) {
	s.mu.Lock()
	delete(s.session, name)
	s.mu.Unlock()
	_ = s.persistent.Delete(name)
}

type osKeyring struct{}

func (osKeyring) Get(name string) (string, error) { return keyring.Get(service, name) }
func (osKeyring) Set(name, value string) error    { return keyring.Set(service, name, value) }
func (osKeyring) Delete(name string) error {
	err := keyring.Delete(service, name)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Memory is an in-memory Backend for tests and for platforms without a keychain.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (m *Memory) Get(name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.m[name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *Memory) Set(name, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[name] = value
	return nil
}

func (m *Memory) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.m, name)
	return nil
}
