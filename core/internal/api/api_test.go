package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sec := secret.NewWithBackend(secret.NewMemory())
	km, _ := keys.NewManager(filepath.Join(dir, "keys"), st, sec)
	known, _ := sshx.NewKnownHosts(filepath.Join(dir, "known_hosts"))
	pool := sshx.NewPool(st, km, sec, known)
	hub := NewHub()
	mon := health.NewMonitor(st, pool, hub.Publish)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := New(ctx, Deps{Token: "secret-token", Store: st, Keys: km, Secrets: sec, Pool: pool, Monitor: mon, Hub: hub})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func call(t *testing.T, ts *httptest.Server, method, path, token, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func TestTokenRequired(t *testing.T) {
	ts := newTestServer(t)
	if res, _ := call(t, ts, "GET", "/api/hosts", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", res.StatusCode)
	}
	if res, _ := call(t, ts, "GET", "/api/hosts", "wrong", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", res.StatusCode)
	}
	if res, _ := call(t, ts, "GET", "/api/hosts", "secret-token", ""); res.StatusCode != http.StatusOK {
		t.Errorf("valid token: %d", res.StatusCode)
	}
	// Preflight must pass without a token so the browser can send the real request.
	res, _ := call(t, ts, "OPTIONS", "/api/hosts", "", "")
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("preflight: %d %v", res.StatusCode, res.Header)
	}
}

func TestHostCRUDAndValidation(t *testing.T) {
	ts := newTestServer(t)
	const tok = "secret-token"

	res, body := call(t, ts, "POST", "/api/hosts", tok, `{"name":"web","address":"","user":"root"}`)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, `"code":"invalid"`) {
		t.Fatalf("validation: %d %s", res.StatusCode, body)
	}

	res, body = call(t, ts, "POST", "/api/hosts", tok, `{"name":"web","address":"10.0.0.9","user":"root","auth":"password","password":"pw","tags":["prod"]}`)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	var h store.Host
	json.Unmarshal([]byte(body), &h)
	if h.ID == "" || h.Port != 22 || strings.Contains(body, "pw") {
		t.Fatalf("created host %s (password must never be echoed)", body)
	}

	res, _ = call(t, ts, "PUT", "/api/hosts/"+h.ID, tok, `{"name":"web-1","address":"10.0.0.9","user":"root","auth":"agent"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update: %d", res.StatusCode)
	}
	if res, _ = call(t, ts, "DELETE", "/api/hosts/"+h.ID, tok, ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if res, body = call(t, ts, "DELETE", "/api/hosts/"+h.ID, tok, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("delete twice: %d %s", res.StatusCode, body)
	}
}

func TestGenerateKeyAndExecValidation(t *testing.T) {
	ts := newTestServer(t)
	const tok = "secret-token"
	res, body := call(t, ts, "POST", "/api/keys/generate", tok, `{"name":"deploy","type":"ed25519"}`)
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, "ssh-ed25519 ") {
		t.Fatalf("generate: %d %s", res.StatusCode, body)
	}
	res, _ = call(t, ts, "POST", "/api/exec", tok, `{"hostIds":[],"command":"uptime"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("exec without hosts: %d", res.StatusCode)
	}
}

func TestDeployScriptQuoting(t *testing.T) {
	s := deployScript(`ssh-ed25519 AAAA it's me`)
	if !strings.Contains(s, `KEY='ssh-ed25519 AAAA it'\''s me'`) {
		t.Errorf("single quote not escaped:\n%s", s)
	}
}
