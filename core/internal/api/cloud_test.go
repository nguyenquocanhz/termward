package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenquocanhz/termward/core/internal/cloud"
	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// newCloudTestServer serves the core API with a Termward Pro client that
// talks to upstream (a stand-in for Termward Cloud).
func newCloudTestServer(t *testing.T, upstream http.HandlerFunc) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
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
	pro, err := cloud.New(cloud.Config{
		DataDir: dir, Secrets: sec, Version: "0.3.0", BaseURL: up.URL, Platform: "linux",
		SystemMachineID: func() (string, error) { return "test-machine", nil },
		Logf:            t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := New(ctx, Deps{Token: "secret-token", Store: st, Keys: km, Secrets: sec, Pool: pool, Monitor: mon, Hub: hub, Cloud: pro})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestCloudRoutes(t *testing.T) {
	const tok = "secret-token"
	ts := newCloudTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/start":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/auth/verify":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"device_limit","message":"too many devices","max":3,"replaceToken":"tok_1",` +
				`"devices":[{"id":"dev_1","name":"A","platform":"windows","appVersion":"0.3.0","current":false}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found","message":"nope"}`))
		}
	})

	res, body := call(t, ts, "GET", "/api/cloud/status", tok, "")
	if res.StatusCode != 200 || !strings.Contains(body, `"signedIn":false`) || !strings.Contains(body, `"channels":[]`) {
		t.Fatalf("status: %d %s", res.StatusCode, body)
	}

	res, body = call(t, ts, "POST", "/api/cloud/signin/start", tok, `{"email":"not-an-email"}`)
	if res.StatusCode != 400 || !strings.Contains(body, `"code":"invalid_email"`) || !strings.Contains(body, `"field":"email"`) {
		t.Fatalf("invalid email: %d %s", res.StatusCode, body)
	}
	if res, body = call(t, ts, "POST", "/api/cloud/signin/start", tok, `{"email":"a@b.vn","lang":"vi"}`); res.StatusCode != 200 {
		t.Fatalf("start: %d %s", res.StatusCode, body)
	}

	res, body = call(t, ts, "POST", "/api/cloud/signin/verify", tok, `{"email":"a@b.vn","code":"123456"}`)
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Max          int            `json:"max"`
				ReplaceToken string         `json:"replaceToken"`
				Devices      []cloud.Device `json:"devices"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &e)
	if res.StatusCode != 409 || e.Error.Code != "device_limit" || e.Error.Details.Max != 3 ||
		e.Error.Details.ReplaceToken != "tok_1" || len(e.Error.Details.Devices) != 1 {
		t.Fatalf("device limit: %d %s", res.StatusCode, body)
	}

	// Signed endpoints need a sign-in first.
	res, body = call(t, ts, "POST", "/api/cloud/checkout", tok, `{"months":1}`)
	if res.StatusCode != 409 || !strings.Contains(body, `"code":"not_signed_in"`) {
		t.Fatalf("checkout signed out: %d %s", res.StatusCode, body)
	}
	res, body = call(t, ts, "POST", "/api/cloud/channels", tok, `{"kind":"slack","webhookUrl":"https://evil.example/x"}`)
	if res.StatusCode != 400 || !strings.Contains(body, `"field":"webhookUrl"`) {
		t.Fatalf("channel validation: %d %s", res.StatusCode, body)
	}

	res, body = call(t, ts, "PUT", "/api/cloud/forwarding", tok, `{"critical":true,"warnings":false,"recoveries":true,"lang":"en-US"}`)
	if res.StatusCode != 200 || !strings.Contains(body, `"forwarding":{"critical":true,"warnings":false,"recoveries":true,"lang":"en"}`) {
		t.Fatalf("forwarding: %d %s", res.StatusCode, body)
	}
	for _, leak := range []string{"secret", "privateKey", "seed"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(leak)) {
			t.Errorf("status mentions %q: %s", leak, body)
		}
	}
}

func TestCloudRoutesWithoutService(t *testing.T) {
	ts := newTestServer(t)
	res, body := call(t, ts, "GET", "/api/cloud/status", "secret-token", "")
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "cloud_disabled") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

func TestCloudFailMapsStatus(t *testing.T) {
	for _, c := range []struct {
		in   *cloud.Error
		want int
	}{
		{&cloud.Error{Status: 401, Code: "bad_signature"}, http.StatusForbidden},
		{&cloud.Error{Status: 402, Code: "plan_inactive", PaidUntil: "2026-01-01T00:00:00Z"}, http.StatusPaymentRequired},
		{&cloud.Error{Status: 0, Code: "x"}, http.StatusBadGateway},
		{&cloud.Error{Status: 429, Code: "rate_limited", RetryAfter: 30}, http.StatusTooManyRequests},
	} {
		w := httptest.NewRecorder()
		cloudFail(w, c.in)
		if w.Code != c.want {
			t.Errorf("%s: %d, want %d", c.in.Code, w.Code, c.want)
		}
		if c.in.RetryAfter > 0 && !strings.Contains(w.Body.String(), `"retryAfter":30`) {
			t.Errorf("retryAfter missing: %s", w.Body)
		}
		if c.in.PaidUntil != "" && !strings.Contains(w.Body.String(), `"paidUntil"`) {
			t.Errorf("paidUntil missing: %s", w.Body)
		}
	}
}
