package sshconfig

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// key returns a distinct, well-formed (if meaningless) key field.
func key(seed string) string {
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("key-"+seed))
}

func parse(t *testing.T, lines ...string) KnownHosts {
	t.Helper()
	kh, err := ParseKnownHosts(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return kh
}

func TestKnownHostsBasics(t *testing.T) {
	kh := parse(t,
		"# a comment",
		"",
		"web.example.com "+key("a"),
		"203.0.113.7 "+key("b"),
		"[203.0.113.8]:2222 "+key("c"),
		"[db.example.com]:2200 "+key("d"),
	)
	want := []KnownHost{
		{Name: "web.example.com", Address: "web.example.com", Port: 22},
		{Name: "203.0.113.7", Address: "203.0.113.7", Port: 22},
		{Name: "203.0.113.8", Address: "203.0.113.8", Port: 2222},
		{Name: "db.example.com", Address: "db.example.com", Port: 2200},
	}
	if len(kh.Hosts) != len(want) {
		t.Fatalf("hosts = %+v", kh.Hosts)
	}
	for i, w := range want {
		if kh.Hosts[i] != w {
			t.Errorf("host %d = %+v, want %+v", i, kh.Hosts[i], w)
		}
	}
	if kh.Hashed != 0 || kh.Invalid != 0 {
		t.Errorf("hashed %d invalid %d", kh.Hashed, kh.Invalid)
	}
}

func TestKnownHostsSkipsWhatIsNotAServer(t *testing.T) {
	kh := parse(t,
		"|1|c2FsdHNhbHQ=|aGFzaGhhc2g= "+key("h1"),
		"|1|c2FsdHNhbHQy|aGFzaGhhc2gy "+key("h2"),
		"@revoked old.example.com "+key("r"),
		"@cert-authority *.example.com "+key("ca"),
		"*.internal,!bastion.internal "+key("w"),
		"this is not a known_hosts line",
		"short ssh-ed25519",
		"host.example.com ssh-ed25519 not*base64",
		"real.example.com "+key("ok"),
	)
	if len(kh.Hosts) != 1 || kh.Hosts[0].Address != "real.example.com" {
		t.Fatalf("hosts = %+v", kh.Hosts)
	}
	if kh.Hashed != 2 {
		t.Errorf("hashed = %d, want 2", kh.Hashed)
	}
	if kh.Invalid != 3 {
		t.Errorf("invalid = %d, want 3", kh.Invalid)
	}
}

func TestKnownHostsFoldsNamesSharingAKey(t *testing.T) {
	kh := parse(t,
		// One line, name and IP.
		"app.example.com,198.51.100.4 "+key("app"),
		// The same server again with another key type.
		"app.example.com,198.51.100.4 ssh-rsa "+strings.TrimPrefix(key("app-rsa"), "ssh-ed25519 "),
		// Name and IP on separate lines, joined only by the key.
		"mail.example.com "+key("mail"),
		"198.51.100.9 "+key("mail"),
		// Two names, no address.
		"git.example.com "+key("git"),
		"code.example.com "+key("git"),
		// A plain duplicate.
		"198.51.100.20 "+key("dup"),
		"198.51.100.20 "+key("dup"),
	)
	want := []KnownHost{
		{Name: "app.example.com", Address: "198.51.100.4", Port: 22},
		{Name: "mail.example.com", Address: "198.51.100.9", Port: 22},
		{Name: "git.example.com", Address: "git.example.com", Port: 22},
		{Name: "198.51.100.20", Address: "198.51.100.20", Port: 22},
	}
	if len(kh.Hosts) != len(want) {
		t.Fatalf("hosts = %+v", kh.Hosts)
	}
	for i, w := range want {
		if kh.Hosts[i] != w {
			t.Errorf("host %d = %+v, want %+v", i, kh.Hosts[i], w)
		}
	}
}

// Machines cloned from one image share a host key but are different servers.
func TestKnownHostsKeepsClonedMachinesApart(t *testing.T) {
	kh := parse(t,
		"node-a,10.0.0.1 "+key("image"),
		"node-b,10.0.0.2 "+key("image"),
		"10.0.0.3 "+key("image"),
	)
	want := []KnownHost{
		{Name: "node-a", Address: "10.0.0.1", Port: 22},
		{Name: "node-b", Address: "10.0.0.2", Port: 22},
		{Name: "10.0.0.3", Address: "10.0.0.3", Port: 22},
	}
	if len(kh.Hosts) != len(want) {
		t.Fatalf("hosts = %+v", kh.Hosts)
	}
	for i, w := range want {
		if kh.Hosts[i] != w {
			t.Errorf("host %d = %+v, want %+v", i, kh.Hosts[i], w)
		}
	}
}

func TestKnownHostsPortsAndIPv6(t *testing.T) {
	kh := parse(t,
		"2001:db8::1 "+key("v6"),
		"[2001:db8::2]:2222 "+key("v6b"),
		"v6.example.com,2001:db8::3 "+key("v6c"),
		// One machine reached through two forwarded ports.
		"[127.0.0.1]:2201 "+key("p1"),
		"[127.0.0.1]:2202 "+key("p2"),
		"[bad]:port "+key("x"),
		"[unclosed:22 "+key("y"),
	)
	want := []KnownHost{
		{Name: "2001:db8::1", Address: "2001:db8::1", Port: 22},
		{Name: "2001:db8::2", Address: "2001:db8::2", Port: 2222},
		{Name: "v6.example.com", Address: "2001:db8::3", Port: 22},
		{Name: "127.0.0.1:2201", Address: "127.0.0.1", Port: 2201},
		{Name: "127.0.0.1:2202", Address: "127.0.0.1", Port: 2202},
	}
	if len(kh.Hosts) != len(want) {
		t.Fatalf("hosts = %+v", kh.Hosts)
	}
	for i, w := range want {
		if kh.Hosts[i] != w {
			t.Errorf("host %d = %+v, want %+v", i, kh.Hosts[i], w)
		}
	}
}

func TestReadKnownHostsFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadKnownHosts(filepath.Join(dir, "nope")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, nil, 0o600)
	kh, err := ReadKnownHosts(empty)
	if err != nil || kh.Hosts == nil || len(kh.Hosts) != 0 {
		t.Fatalf("empty file: %+v %v", kh, err)
	}
	// Windows line endings must not end up in the last field.
	crlf := filepath.Join(dir, "crlf")
	os.WriteFile(crlf, []byte("a.example.com "+key("a")+"\r\nb.example.com "+key("b")+"\r\n"), 0o600)
	if kh, err = ReadKnownHosts(crlf); err != nil || len(kh.Hosts) != 2 || kh.Invalid != 0 {
		t.Fatalf("crlf file: %+v %v", kh, err)
	}
}

func TestReadParseError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config")
	os.WriteFile(p, []byte("Host ok\n  HostName 10.0.0.1\nMatch exec \"true\"\n"), 0o600)
	_, err := Read(p)
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want a ParseError, got %v", err)
	}
	if pe.Line != 3 {
		t.Errorf("line = %d, want 3", pe.Line)
	}
	if strings.Contains(err.Error(), "exec") {
		t.Errorf("parse error quotes the file: %v", err)
	}
}
