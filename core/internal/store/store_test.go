package store

import (
	"strings"
	"testing"
)

func TestHostValidationAndDefaults(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveHost(Host{Address: "", User: "root"}); err == nil {
		t.Error("empty address accepted")
	}
	if _, err := s.SaveHost(Host{Address: "a", User: "root", Port: 70000}); err == nil {
		t.Error("bad port accepted")
	}
	if _, err := s.SaveHost(Host{Address: "a", User: "root", Auth: AuthKey}); err == nil {
		t.Error("key auth without key accepted")
	}
	h, err := s.SaveHost(Host{Address: " 10.0.0.5 ", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "10.0.0.5" || h.Port != 22 || h.Auth != AuthAgent || h.ID == "" || h.Tags == nil {
		t.Errorf("defaults not applied: %+v", h)
	}
}

func TestJumpHostLoopRejected(t *testing.T) {
	s, _ := Open(t.TempDir())
	a, _ := s.SaveHost(Host{Name: "a", Address: "a", User: "u"})
	b, _ := s.SaveHost(Host{Name: "b", Address: "b", User: "u", JumpHostID: a.ID})
	a.JumpHostID = b.ID
	if _, err := s.SaveHost(a); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Fatalf("loop not detected: %v", err)
	}
	// The failed save must not leak into memory.
	if got, _ := s.Host(a.ID); got.JumpHostID != "" {
		t.Errorf("rejected change was kept: %+v", got)
	}
	if err := s.DeleteHost(a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Host(b.ID); got.JumpHostID != "" {
		t.Errorf("dangling jump host after delete: %+v", got)
	}
}

func TestEmptyListsAreNotNil(t *testing.T) {
	s, _ := Open(t.TempDir())
	// The UI iterates these directly; null would crash it.
	if s.Hosts() == nil || s.Keys() == nil || s.Snippets() == nil {
		t.Fatal("empty lists must be [] not nil")
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	h, _ := s.SaveHost(Host{Name: "web", Address: "web", User: "root", Tags: []string{"prod"}})
	if _, err := s.SaveSettings(Settings{PollIntervalSec: 1}); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Host(h.ID)
	if err != nil || got.Tags[0] != "prod" {
		t.Fatalf("host not persisted: %+v %v", got, err)
	}
	st := s2.Settings()
	if st.PollIntervalSec != 10 || st.Thresholds.DiskCrit != 90 {
		t.Errorf("settings not normalized: %+v", st)
	}
}
