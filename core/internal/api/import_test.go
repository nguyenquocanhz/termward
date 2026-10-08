package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenquocanhz/termward/core/internal/store"
)

type importReply struct {
	Path      string `json:"path"`
	Exists    bool   `json:"exists"`
	Error     string `json:"error"`
	ErrorLine int    `json:"errorLine"`
	Entries   []struct {
		Alias   string `json:"alias"`
		Name    string `json:"name"`
		Address string `json:"address"`
		Port    int    `json:"port"`
		Exists  bool   `json:"exists"`
	} `json:"entries"`
	Hashed      int    `json:"hashed"`
	Invalid     int    `json:"invalid"`
	DefaultUser string `json:"defaultUser"`
}

// fakeHome points ~ at an empty directory and returns its .ssh.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ssh := filepath.Join(home, ".ssh")
	if err := os.Mkdir(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	return ssh
}

func getImport(t *testing.T, call func() (*http.Response, string)) importReply {
	t.Helper()
	res, body := call()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	var out importReply
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if !strings.Contains(body, `"entries":[`) {
		t.Fatalf("entries must always be an array: %s", body)
	}
	return out
}

func hostKey(seed string) string {
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("key-"+seed))
}

func TestReadSSHConfigStates(t *testing.T) {
	const tok = "secret-token"
	ssh := fakeHome(t)
	ts := newTestServer(t)
	read := func(path string) importReply {
		if path == "" {
			return getImport(t, func() (*http.Response, string) { return call(t, ts, "GET", "/api/import/ssh-config", tok, "") })
		}
		body, _ := json.Marshal(map[string]string{"path": path})
		return getImport(t, func() (*http.Response, string) {
			return call(t, ts, "POST", "/api/import/ssh-config/read", tok, string(body))
		})
	}
	def := filepath.Join(ssh, "config")

	// No file: say so, with the path that was looked at.
	r := read("")
	if r.Exists || r.Error != "" || len(r.Entries) != 0 || r.Path != def {
		t.Fatalf("missing: %+v", r)
	}

	// Present but empty, and present with only patterns: no hosts, no error.
	os.WriteFile(def, nil, 0o600)
	if r = read(""); !r.Exists || r.Error != "" || len(r.Entries) != 0 {
		t.Fatalf("empty: %+v", r)
	}
	os.WriteFile(def, []byte("Host *\n  User ops\n"), 0o600)
	if r = read(""); !r.Exists || r.Error != "" || len(r.Entries) != 0 {
		t.Fatalf("patterns only: %+v", r)
	}

	// Broken syntax: an error code and a line, never the text of the file.
	os.WriteFile(def, []byte("Host web\n  HostName 10.9.8.7\nMatch exec \"true\"\n"), 0o600)
	res, body := call(t, ts, "GET", "/api/import/ssh-config", tok, "")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"error":"parse","errorLine":3`) || strings.Contains(body, "10.9.8.7") {
		t.Fatalf("parse error: %d %s", res.StatusCode, body)
	}

	// A good file, and one of its hosts already in Termward.
	os.WriteFile(def, []byte("Host web\n  HostName 10.9.8.7\n  User ops\nHost db\n  HostName 10.9.8.8\n  Port 2222\n"), 0o600)
	call(t, ts, "POST", "/api/hosts", tok, `{"name":"web","address":"10.9.8.7","user":"ops","auth":"agent"}`)
	r = read("")
	if len(r.Entries) != 2 || r.Error != "" {
		t.Fatalf("good file: %+v", r)
	}
	for _, e := range r.Entries {
		if e.Exists != (e.Alias == "web") {
			t.Errorf("exists flag of %s = %v", e.Alias, e.Exists)
		}
	}

	// Another file chosen by the user; a directory; a relative path.
	other := filepath.Join(t.TempDir(), "work.conf")
	os.WriteFile(other, []byte("Host jump\n  HostName 192.0.2.1\n"), 0o600)
	if r = read(other); r.Path != other || len(r.Entries) != 1 || r.Entries[0].Alias != "jump" {
		t.Fatalf("chosen file: %+v", r)
	}
	if r = read(ssh); !r.Exists || r.Error != "not_a_file" {
		t.Fatalf("directory: %+v", r)
	}
	if res, _ = call(t, ts, "POST", "/api/import/ssh-config/read", tok, `{"path":"relative/config"}`); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("relative path: %d", res.StatusCode)
	}

	// Importing from the chosen file.
	in, _ := json.Marshal(map[string]any{"aliases": []string{"jump"}, "group": "edge", "path": other})
	res, body = call(t, ts, "POST", "/api/import/ssh-config", tok, string(in))
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"name":"jump"`) || !strings.Contains(body, `"monitor":true`) {
		t.Fatalf("import from chosen file: %d %s", res.StatusCode, body)
	}
	missing, _ := json.Marshal(map[string]any{"aliases": []string{"x"}, "path": filepath.Join(ssh, "config")})
	os.WriteFile(def, []byte("Match exec \"true\"\n"), 0o600)
	if res, _ = call(t, ts, "POST", "/api/import/ssh-config", tok, string(missing)); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("import from a broken file: %d", res.StatusCode)
	}
}

func TestKnownHostsImport(t *testing.T) {
	const tok = "secret-token"
	ssh := fakeHome(t)
	ts := newTestServer(t)
	read := func() importReply {
		return getImport(t, func() (*http.Response, string) { return call(t, ts, "GET", "/api/import/known-hosts", tok, "") })
	}
	def := filepath.Join(ssh, "known_hosts")

	r := read()
	if r.Exists || r.Path != def || len(r.Entries) != 0 {
		t.Fatalf("missing: %+v", r)
	}

	os.WriteFile(def, nil, 0o600)
	if r = read(); !r.Exists || r.Error != "" || len(r.Entries) != 0 || r.Hashed != 0 {
		t.Fatalf("empty: %+v", r)
	}

	// Something that is not a known_hosts at all.
	os.WriteFile(def, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nc2VjcmV0\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600)
	res, body := call(t, ts, "GET", "/api/import/known-hosts", tok, "")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"error":"parse"`) || strings.Contains(body, "c2VjcmV0") {
		t.Fatalf("not known_hosts: %d %s", res.StatusCode, body)
	}

	os.WriteFile(def, []byte(strings.Join([]string{
		"|1|c2FsdA==|aGFzaA== " + hostKey("hashed"),
		"app.example.com,198.51.100.4 " + hostKey("app"),
		"[198.51.100.5]:2222 " + hostKey("alt"),
		"[2001:db8::7]:2200 " + hostKey("v6"),
		"198.51.100.4 " + hostKey("app"),
		"@revoked gone.example.com " + hostKey("gone"),
	}, "\n")+"\n"), 0o600)
	call(t, ts, "POST", "/api/hosts", tok, `{"name":"alt","address":"198.51.100.5","port":2222,"user":"ops","auth":"agent"}`)
	r = read()
	if len(r.Entries) != 3 || r.Hashed != 1 || r.Invalid != 0 || r.Error != "" {
		t.Fatalf("list: %+v", r)
	}
	if e := r.Entries[0]; e.Name != "app.example.com" || e.Address != "198.51.100.4" || e.Port != 22 || e.Exists {
		t.Errorf("folded entry = %+v", e)
	}
	if e := r.Entries[1]; e.Address != "198.51.100.5" || e.Port != 2222 || !e.Exists {
		t.Errorf("already added entry = %+v", e)
	}
	if e := r.Entries[2]; e.Address != "2001:db8::7" || e.Port != 2200 {
		t.Errorf("ipv6 entry = %+v", e)
	}
	if r.DefaultUser == "" {
		t.Error("no default user suggested")
	}
	if strings.Contains(body, "ssh-ed25519") {
		t.Error("host keys must not be returned")
	}

	if res, _ = call(t, ts, "POST", "/api/import/known-hosts", tok, `{"hosts":[]}`); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty selection: %d", res.StatusCode)
	}
	res, body = call(t, ts, "POST", "/api/import/known-hosts", tok, `{"hosts":[
		{"name":"app.example.com","address":"198.51.100.4","port":22,"user":"deploy","group":"prod"},
		{"name":"alt","address":"198.51.100.5","port":2222,"user":"ops"},
		{"name":"v6","address":"2001:db8::7","port":2200,"user":""}]}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("import: %d %s", res.StatusCode, body)
	}
	var out struct {
		Imported []store.Host      `json:"imported"`
		Skipped  []string          `json:"skipped"`
		Failed   map[string]string `json:"failed"`
	}
	json.Unmarshal([]byte(body), &out)
	if len(out.Imported) != 1 || len(out.Skipped) != 1 || len(out.Failed) != 1 {
		t.Fatalf("import result: %s", body)
	}
	h := out.Imported[0]
	if h.Name != "app.example.com" || h.Address != "198.51.100.4" || h.User != "deploy" || h.Group != "prod" {
		t.Errorf("imported host = %+v", h)
	}
	if h.Monitor {
		t.Error("hosts from known_hosts must start unmonitored")
	}
	if h.Auth != store.AuthAgent {
		t.Errorf("auth = %q", h.Auth)
	}
	if _, ok := out.Failed["v6"]; !ok {
		t.Errorf("a host without a user must fail: %v", out.Failed)
	}
}

func TestKnownHostsChosenFile(t *testing.T) {
	const tok = "secret-token"
	fakeHome(t)
	ts := newTestServer(t)
	other := filepath.Join(t.TempDir(), "known_hosts.old")
	os.WriteFile(other, []byte("[legacy.example.com]:2022 "+hostKey("legacy")+"\n"), 0o600)
	body, _ := json.Marshal(map[string]string{"path": other})
	r := getImport(t, func() (*http.Response, string) {
		return call(t, ts, "POST", "/api/import/known-hosts/read", tok, string(body))
	})
	if r.Path != other || !r.Exists || len(r.Entries) != 1 || r.Entries[0].Address != "legacy.example.com" || r.Entries[0].Port != 2022 {
		t.Fatalf("chosen known_hosts: %+v", r)
	}
}
