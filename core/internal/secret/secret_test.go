package secret

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFileBackendRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.bin")
	key := bytes.Repeat([]byte{7}, 32)

	f, err := NewFileBackend(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Set(HostPassword("h1"), "hunter2"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte("hunter2")) {
		t.Fatal("secret stored in plain text")
	}

	g, err := NewFileBackend(path, key)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := g.Get(HostPassword("h1")); err != nil || v != "hunter2" {
		t.Fatalf("reload: %q %v", v, err)
	}
	if _, err := NewFileBackend(path, bytes.Repeat([]byte{8}, 32)); err == nil {
		t.Fatal("wrong key must not decrypt")
	}
	if err := g.Delete(HostPassword("h1")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Get(HostPassword("h1")); err != ErrNotFound {
		t.Fatalf("deleted secret still there: %v", err)
	}
}

func TestSessionOnlyUnlessRemembered(t *testing.T) {
	mem := NewMemory()
	s := NewWithBackend(mem)
	s.Put("a", "1", false)
	s.Put("b", "2", true)
	if _, err := mem.Get("a"); err == nil {
		t.Error("non-remembered secret reached the persistent store")
	}
	if v, _ := mem.Get("b"); v != "2" {
		t.Error("remembered secret was not persisted")
	}
	if v, _ := s.Get("a"); v != "1" {
		t.Error("session secret lost")
	}
}
