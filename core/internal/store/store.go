// Package store persists hosts, key metadata, snippets and settings as a single
// JSON document. Writes are atomic (temp file + rename) so a crash or power loss
// never leaves a half-written file behind.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const schemaVersion = 1

var ErrNotFound = errors.New("not found")

type AuthMethod string

const (
	AuthKey      AuthMethod = "key"
	AuthPassword AuthMethod = "password"
	AuthAgent    AuthMethod = "agent"
)

type Host struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Address    string     `json:"address"`
	Port       int        `json:"port"`
	User       string     `json:"user"`
	Group      string     `json:"group"`
	Tags       []string   `json:"tags"`
	Auth       AuthMethod `json:"auth"`
	KeyID      string     `json:"keyId,omitempty"`
	JumpHostID string     `json:"jumpHostId,omitempty"`
	Monitor    bool       `json:"monitor"`
	Notes      string     `json:"notes,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

type Key struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Bits        int       `json:"bits"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"publicKey"`
	Comment     string    `json:"comment"`
	Encrypted   bool      `json:"encrypted"`
	Source      string    `json:"source"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Snippet struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Command   string    `json:"command"`
	CreatedAt time.Time `json:"createdAt"`
}

type Thresholds struct {
	CPUWarn         float64 `json:"cpuWarn"`
	CPUCrit         float64 `json:"cpuCrit"`
	MemWarn         float64 `json:"memWarn"`
	MemCrit         float64 `json:"memCrit"`
	DiskWarn        float64 `json:"diskWarn"`
	DiskCrit        float64 `json:"diskCrit"`
	LoadPerCoreWarn float64 `json:"loadPerCoreWarn"`
	LoadPerCoreCrit float64 `json:"loadPerCoreCrit"`
}

type Settings struct {
	PollIntervalSec int        `json:"pollIntervalSec"`
	Notifications   bool       `json:"notifications"`
	Thresholds      Thresholds `json:"thresholds"`
}

func DefaultSettings() Settings {
	return Settings{
		PollIntervalSec: 30,
		Notifications:   true,
		Thresholds: Thresholds{
			CPUWarn: 85, CPUCrit: 95,
			MemWarn: 85, MemCrit: 95,
			DiskWarn: 80, DiskCrit: 90,
			LoadPerCoreWarn: 1.5, LoadPerCoreCrit: 3,
		},
	}
}

type document struct {
	Version  int       `json:"version"`
	Hosts    []Host    `json:"hosts"`
	Keys     []Key     `json:"keys"`
	Snippets []Snippet `json:"snippets"`
	Settings Settings  `json:"settings"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	doc  document
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "termward.json")}
	s.doc = document{Version: schemaVersion, Settings: DefaultSettings()}

	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, s.saveLocked()
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &s.doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	s.doc.Settings = normalizeSettings(s.doc.Settings)
	return s, nil
}

func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// ---------------------------------------------------------------- hosts

func (s *Store) Hosts() []Host {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Host, len(s.doc.Hosts))
	for i, h := range s.doc.Hosts {
		out[i] = cloneHost(h)
	}
	return out
}

func (s *Store) Host(id string) (Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, h := range s.doc.Hosts {
		if h.ID == id {
			return cloneHost(h), nil
		}
	}
	return Host{}, ErrNotFound
}

func (s *Store) SaveHost(h Host) (Host, error) {
	if err := validateHost(&h); err != nil {
		return Host{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	h.UpdatedAt = now
	hosts := slices.Clone(s.doc.Hosts)
	if h.ID == "" {
		h.ID = NewID()
		h.CreatedAt = now
		hosts = append(hosts, h)
	} else {
		i := slices.IndexFunc(hosts, func(x Host) bool { return x.ID == h.ID })
		if i < 0 {
			return Host{}, ErrNotFound
		}
		h.CreatedAt = hosts[i].CreatedAt
		hosts[i] = h
	}
	if h.JumpHostID != "" {
		if !slices.ContainsFunc(hosts, func(x Host) bool { return x.ID == h.JumpHostID }) {
			return Host{}, errors.New("jump host does not exist")
		}
		if jumpCycle(hosts, h.ID) {
			return Host{}, errors.New("jump host chain forms a loop")
		}
	}
	s.doc.Hosts = hosts
	return cloneHost(h), s.saveLocked()
}

func jumpCycle(hosts []Host, start string) bool {
	seen := map[string]bool{}
	for cur := start; cur != ""; {
		if seen[cur] {
			return true
		}
		seen[cur] = true
		next := ""
		for _, h := range hosts {
			if h.ID == cur {
				next = h.JumpHostID
				break
			}
		}
		cur = next
	}
	return false
}

func (s *Store) DeleteHost(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.doc.Hosts, func(x Host) bool { return x.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	s.doc.Hosts = slices.Delete(s.doc.Hosts, i, i+1)
	for j := range s.doc.Hosts {
		if s.doc.Hosts[j].JumpHostID == id {
			s.doc.Hosts[j].JumpHostID = ""
		}
	}
	return s.saveLocked()
}

func validateHost(h *Host) error {
	h.Name = strings.TrimSpace(h.Name)
	h.Address = strings.TrimSpace(h.Address)
	h.User = strings.TrimSpace(h.User)
	h.Group = strings.TrimSpace(h.Group)
	if h.Address == "" {
		return errors.New("address is required")
	}
	if h.Name == "" {
		h.Name = h.Address
	}
	if h.Port == 0 {
		h.Port = 22
	}
	if h.Port < 1 || h.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if h.User == "" {
		return errors.New("user is required")
	}
	switch h.Auth {
	case AuthKey:
		if h.KeyID == "" {
			return errors.New("select a key for key authentication")
		}
	case AuthPassword, AuthAgent:
		h.KeyID = ""
	case "":
		h.Auth = AuthAgent
	default:
		return fmt.Errorf("unknown auth method %q", h.Auth)
	}
	if h.JumpHostID == h.ID && h.ID != "" {
		return errors.New("a host cannot be its own jump host")
	}
	if h.Tags == nil {
		h.Tags = []string{}
	}
	return nil
}

func cloneHost(h Host) Host {
	h.Tags = slices.Clone(h.Tags)
	if h.Tags == nil {
		h.Tags = []string{}
	}
	return h
}

// ---------------------------------------------------------------- keys

func (s *Store) Keys() []Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return nonNil(slices.Clone(s.doc.Keys))
}

// nonNil makes empty lists encode as [] instead of null in JSON.
func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func (s *Store) Key(id string) (Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.doc.Keys {
		if k.ID == id {
			return k, nil
		}
	}
	return Key{}, ErrNotFound
}

func (s *Store) KeyByFingerprint(fp string) (Key, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.doc.Keys {
		if k.Fingerprint == fp {
			return k, true
		}
	}
	return Key{}, false
}

func (s *Store) AddKey(k Key) (Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k.ID == "" {
		k.ID = NewID()
	}
	k.CreatedAt = time.Now().UTC()
	s.doc.Keys = append(s.doc.Keys, k)
	return k, s.saveLocked()
}

func (s *Store) RenameKey(id, name string) (Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.doc.Keys, func(x Key) bool { return x.ID == id })
	if i < 0 {
		return Key{}, ErrNotFound
	}
	s.doc.Keys[i].Name = strings.TrimSpace(name)
	return s.doc.Keys[i], s.saveLocked()
}

// DeleteKey refuses to remove a key that hosts still authenticate with.
func (s *Store) DeleteKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var users []string
	for _, h := range s.doc.Hosts {
		if h.Auth == AuthKey && h.KeyID == id {
			users = append(users, h.Name)
		}
	}
	if len(users) > 0 {
		return fmt.Errorf("key is used by: %s", strings.Join(users, ", "))
	}
	i := slices.IndexFunc(s.doc.Keys, func(x Key) bool { return x.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	s.doc.Keys = slices.Delete(s.doc.Keys, i, i+1)
	return s.saveLocked()
}

// ---------------------------------------------------------------- snippets

func (s *Store) Snippets() []Snippet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return nonNil(slices.Clone(s.doc.Snippets))
}

func (s *Store) SaveSnippet(sn Snippet) (Snippet, error) {
	sn.Name = strings.TrimSpace(sn.Name)
	if sn.Name == "" || strings.TrimSpace(sn.Command) == "" {
		return Snippet{}, errors.New("name and command are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sn.ID == "" {
		sn.ID = NewID()
		sn.CreatedAt = time.Now().UTC()
		s.doc.Snippets = append(s.doc.Snippets, sn)
	} else {
		i := slices.IndexFunc(s.doc.Snippets, func(x Snippet) bool { return x.ID == sn.ID })
		if i < 0 {
			return Snippet{}, ErrNotFound
		}
		sn.CreatedAt = s.doc.Snippets[i].CreatedAt
		s.doc.Snippets[i] = sn
	}
	return sn, s.saveLocked()
}

func (s *Store) DeleteSnippet(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.doc.Snippets, func(x Snippet) bool { return x.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	s.doc.Snippets = slices.Delete(s.doc.Snippets, i, i+1)
	return s.saveLocked()
}

// ---------------------------------------------------------------- settings

func (s *Store) Settings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.doc.Settings
}

func (s *Store) SaveSettings(v Settings) (Settings, error) {
	v = normalizeSettings(v)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doc.Settings = v
	return v, s.saveLocked()
}

func normalizeSettings(v Settings) Settings {
	d := DefaultSettings()
	if v.PollIntervalSec == 0 {
		v.PollIntervalSec = d.PollIntervalSec
	}
	v.PollIntervalSec = min(max(v.PollIntervalSec, 10), 3600)
	fix := func(p *float64, def float64) {
		if *p <= 0 {
			*p = def
		}
	}
	t := &v.Thresholds
	fix(&t.CPUWarn, d.Thresholds.CPUWarn)
	fix(&t.CPUCrit, d.Thresholds.CPUCrit)
	fix(&t.MemWarn, d.Thresholds.MemWarn)
	fix(&t.MemCrit, d.Thresholds.MemCrit)
	fix(&t.DiskWarn, d.Thresholds.DiskWarn)
	fix(&t.DiskCrit, d.Thresholds.DiskCrit)
	fix(&t.LoadPerCoreWarn, d.Thresholds.LoadPerCoreWarn)
	fix(&t.LoadPerCoreCrit, d.Thresholds.LoadPerCoreCrit)
	return v
}
