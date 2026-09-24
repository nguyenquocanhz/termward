package sshconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	os.WriteFile(p, []byte(`
Host bastion
    HostName 203.0.113.10
    User ops
    Port 2222

Host app-1 app-2
    HostName 10.0.0.%h
    ProxyJump ops@bastion:2222
    IdentityFile ~/.ssh/id_app

Host *.internal
    User deploy

Host *
    User fallback
`), 0o600)

	entries, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 concrete hosts, got %+v", entries)
	}
	byAlias := map[string]Entry{}
	for _, e := range entries {
		byAlias[e.Alias] = e
	}
	b := byAlias["bastion"]
	if b.HostName != "203.0.113.10" || b.User != "ops" || b.Port != 2222 {
		t.Errorf("bastion = %+v", b)
	}
	a := byAlias["app-1"]
	if a.HostName != "10.0.0.app-1" || a.User != "fallback" || a.Port != 22 || a.ProxyJump != "ops@bastion:2222" {
		t.Errorf("app-1 = %+v", a)
	}
	home, _ := os.UserHomeDir()
	if a.IdentityFile != filepath.Join(home, ".ssh", "id_app") {
		t.Errorf("identity = %q", a.IdentityFile)
	}
}

func TestFirstJumpAlias(t *testing.T) {
	for in, want := range map[string]string{
		"bastion":          "bastion",
		"ops@bastion:2222": "bastion",
		"a@hop1:22,b@hop2": "hop1",
		" jump ":           "jump",
	} {
		if got := FirstJumpAlias(in); got != want {
			t.Errorf("FirstJumpAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMissingFile(t *testing.T) {
	entries, err := Read(filepath.Join(t.TempDir(), "nope"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing config should be empty, got %v %v", entries, err)
	}
}
