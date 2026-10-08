package cloud

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCloud is a small Termward Cloud for tests. Like the real server it
// verifies TW2 signatures (canonical string, clock skew, replay, revoked and
// mismatched devices) and registration proofs; a request it cannot verify
// fails the way the contract says.
type fakeCloud struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	clock      func() time.Time
	maxDevices int
	devices    []*fakeDevice
	codes      map[string]string
	nonces     map[string]bool
	tokens     map[string]string // replaceToken -> email
	planActive bool
	paidUntil  string
	channels   []Channel
	orders     map[int64]*Order
	nextOrder  int64
	batches    [][]Event
	// alertResult decides the outcome per event (default: delivered to every channel).
	alertResult func(e Event) (delivered int, failed bool)
	// alertError makes /v1/alerts fail with this status/body.
	alertError func() (int, string)
	// retryAfter is the retryAfter of alertError answers (default 120).
	retryAfter int64
	log        []string
	signedOK   int
}

type fakeDevice struct {
	id, email, pubStr, machine, name, platform, version string
	pub                                                 ed25519.PublicKey
	revoked                                             bool
	created                                             time.Time
}

func newFakeCloud(t *testing.T) *fakeCloud {
	f := &fakeCloud{
		t: t, clock: time.Now, maxDevices: 3,
		codes: map[string]string{}, nonces: map[string]bool{}, tokens: map[string]string{},
		orders: map[int64]*Order{}, nextOrder: 175948000042,
	}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCloud) fail(w http.ResponseWriter, status int, code string, extra map[string]any) {
	body := map[string]any{"error": code, "message": code}
	for k, v := range extra {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeCloud) ok(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var b64Re = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// verify checks a signed request like src/signed.ts. Must hold f.mu.
func (f *fakeCloud) verify(w http.ResponseWriter, r *http.Request, body []byte) *fakeDevice {
	h := r.Header
	dev, machine, ts, nonce, sig := h.Get(HeaderDevice), h.Get(HeaderMachine), h.Get(HeaderTime), h.Get(HeaderNonce), h.Get(HeaderSignature)
	if dev == "" || machine == "" || ts == "" || nonce == "" || sig == "" {
		f.fail(w, 401, "bad_signature", nil)
		return nil
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(machine) || !regexp.MustCompile(`^\d{1,12}$`).MatchString(ts) ||
		!b64Re.MatchString(nonce) || !b64Re.MatchString(sig) || strings.Contains(sig, "=") {
		f.fail(w, 401, "bad_signature", nil)
		return nil
	}
	nb, err1 := b64.DecodeString(nonce)
	sb, err2 := b64.DecodeString(sig)
	if err1 != nil || err2 != nil || len(nb) != 16 || len(sb) != 64 {
		f.fail(w, 401, "bad_signature", nil)
		return nil
	}
	var d *fakeDevice
	for _, x := range f.devices {
		if x.id == dev {
			d = x
		}
	}
	if d == nil || d.revoked {
		f.fail(w, 401, "device_revoked", nil)
		return nil
	}
	now := f.clock().Unix()
	t, _ := strconv.ParseInt(ts, 10, 64)
	if now-t > 300 || t-now > 300 {
		f.fail(w, 401, "clock_skew", map[string]any{"serverTime": now})
		return nil
	}
	canon := Canonical(r.Method, r.URL.RequestURI(), dev, ts, nonce, machine, body)
	if !ed25519.Verify(d.pub, []byte(canon), sb) {
		f.fail(w, 401, "bad_signature", nil)
		return nil
	}
	if machine != d.machine {
		f.fail(w, 401, "device_mismatch", nil)
		return nil
	}
	if f.nonces[d.pubStr+nonce] {
		f.fail(w, 401, "replay", nil)
		return nil
	}
	f.nonces[d.pubStr+nonce] = true
	f.signedOK++
	return d
}

// register validates a device object and its proof. Must hold f.mu.
func (f *fakeCloud) checkDevice(w http.ResponseWriter, raw map[string]any, subject string) (DeviceInfo, bool) {
	b, _ := json.Marshal(raw)
	var d DeviceInfo
	_ = json.Unmarshal(b, &d)
	pub, err := b64.DecodeString(d.PublicKey)
	if err != nil || len(pub) != 32 || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(d.Machine) ||
		!map[string]bool{"windows": true, "macos": true, "linux": true, "android": true, "ios": true}[d.Platform] ||
		!versionRE.MatchString(d.AppVersion) {
		f.fail(w, 400, "invalid_device", nil)
		return d, false
	}
	sig, err := b64.DecodeString(d.Proof)
	if err != nil || len(sig) != 64 || !ed25519.Verify(pub, []byte(RegistrationMessage(subject, d.PublicKey, d.Machine)), sig) {
		f.fail(w, 400, "invalid_proof", nil)
		return d, false
	}
	return d, true
}

func (f *fakeCloud) active(email string) []*fakeDevice {
	var out []*fakeDevice
	for _, d := range f.devices {
		if d.email == email && !d.revoked {
			out = append(out, d)
		}
	}
	return out
}

func (f *fakeCloud) deviceJSON(d *fakeDevice, current string) Device {
	return Device{ID: d.id, Name: d.name, Platform: d.platform, AppVersion: d.version,
		CreatedAt: d.created.UTC().Format(time.RFC3339), Current: d.id == current}
}

func (f *fakeCloud) addDevice(email string, d DeviceInfo) *fakeDevice {
	for _, x := range f.active(email) {
		if x.pubStr == d.PublicKey {
			return x // idempotent re-registration
		}
	}
	pub, _ := b64.DecodeString(d.PublicKey)
	nd := &fakeDevice{
		id: fmt.Sprintf("dev_%d", len(f.devices)+1), email: email, pubStr: d.PublicKey, pub: pub,
		machine: d.Machine, name: d.Name, platform: d.Platform, version: d.AppVersion, created: f.clock(),
	}
	f.devices = append(f.devices, nd)
	return nd
}

func (f *fakeCloud) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, r.Method+" "+r.URL.RequestURI())
	var in map[string]any
	if len(body) > 0 {
		if json.Unmarshal(body, &in) != nil {
			f.fail(w, 400, "invalid_json", nil)
			return
		}
	}
	str := func(k string) string { s, _ := in[k].(string); return s }
	path := r.URL.Path

	switch {
	case r.Method == "POST" && path == "/v1/auth/start":
		email, ok := NormalizeEmail(str("email"))
		if !ok {
			f.fail(w, 400, "invalid_email", nil)
			return
		}
		f.codes[email] = "123456"
		f.ok(w, 200, map[string]bool{"ok": true})

	case r.Method == "POST" && path == "/v1/auth/verify":
		email, _ := NormalizeEmail(str("email"))
		devRaw, _ := in["device"].(map[string]any)
		d, ok := f.checkDevice(w, devRaw, email)
		if !ok {
			return
		}
		if f.codes[email] == "" || f.codes[email] != str("code") {
			f.fail(w, 400, "invalid_code", nil)
			return
		}
		delete(f.codes, email)
		act := f.active(email)
		known := false
		for _, x := range act {
			known = known || x.pubStr == d.PublicKey
		}
		if !known && len(act) >= f.maxDevices {
			tok := fmt.Sprintf("tok_%d", len(f.tokens)+1)
			f.tokens[tok] = email
			list := []Device{}
			for _, x := range act {
				list = append(list, f.deviceJSON(x, ""))
			}
			f.fail(w, 409, "device_limit", map[string]any{"max": f.maxDevices, "devices": list, "replaceToken": tok})
			return
		}
		nd := f.addDevice(email, d)
		f.ok(w, 200, map[string]string{"accountId": "acc_1", "deviceId": nd.id, "email": email})

	case r.Method == "POST" && path == "/v1/auth/replace":
		tok := str("replaceToken")
		devRaw, _ := in["device"].(map[string]any)
		d, ok := f.checkDevice(w, devRaw, tok)
		if !ok {
			return
		}
		email, ok := f.tokens[tok]
		if !ok {
			f.fail(w, 400, "invalid_replace_token", nil)
			return
		}
		delete(f.tokens, tok)
		for _, x := range f.active(email) {
			if x.id == str("revokeDeviceId") {
				x.revoked = true
			}
		}
		nd := f.addDevice(email, d)
		f.ok(w, 200, map[string]string{"accountId": "acc_1", "deviceId": nd.id, "email": email})

	case r.Method == "GET" && path == "/v1/me":
		d := f.verify(w, r, body)
		if d == nil {
			return
		}
		list := []Device{}
		for _, x := range f.active(d.email) {
			list = append(list, f.deviceJSON(x, d.id))
		}
		var paid any
		if f.paidUntil != "" {
			paid = f.paidUntil
		}
		f.ok(w, 200, map[string]any{
			"account":  map[string]string{"id": "acc_1", "email": d.email},
			"plan":     map[string]any{"active": f.planActive, "paidUntil": paid, "maxDevices": f.maxDevices},
			"device":   map[string]string{"id": d.id, "name": d.name},
			"devices":  list,
			"channels": append([]Channel{}, f.channels...),
			"prices": []Price{
				{Months: 1, Amount: 260000, VAT: 26000, Total: 286000, Currency: "VND"},
				{Months: 3, Amount: 780000, VAT: 78000, Total: 858000, Currency: "VND"},
			},
			"limits": map[string]int{"alertsPerDay": 500, "usedToday": 0},
		})

	case r.Method == "DELETE" && strings.HasPrefix(path, "/v1/devices/"):
		d := f.verify(w, r, body)
		if d == nil {
			return
		}
		id := strings.TrimPrefix(path, "/v1/devices/")
		for _, x := range f.active(d.email) {
			if x.id == id {
				x.revoked = true
				w.WriteHeader(204)
				return
			}
		}
		f.fail(w, 404, "not_found", nil)

	case r.Method == "POST" && path == "/v1/checkout":
		if f.verify(w, r, body) == nil {
			return
		}
		months, _ := in["months"].(float64)
		code := f.nextOrder
		f.nextOrder++
		o := &Order{OrderCode: code, Status: OrderPending, Months: int(months), Amount: 286000 * int64(months),
			CheckoutURL: "https://pay.payos.vn/web/" + strconv.FormatInt(code, 10), ExpiresAt: f.clock().Add(30 * time.Minute).UTC().Format(time.RFC3339)}
		f.orders[code] = o
		f.ok(w, 200, o)

	case r.Method == "GET" && strings.HasPrefix(path, "/v1/orders/"):
		if f.verify(w, r, body) == nil {
			return
		}
		code, _ := strconv.ParseInt(strings.TrimPrefix(path, "/v1/orders/"), 10, 64)
		o, ok := f.orders[code]
		if !ok {
			f.fail(w, 404, "not_found", nil)
			return
		}
		cp := *o
		cp.Plan = &Plan{Active: f.planActive, PaidUntil: f.paidUntil}
		f.ok(w, 200, cp)

	case r.Method == "POST" && path == "/v1/channels":
		if f.verify(w, r, body) == nil {
			return
		}
		if !f.planActive {
			f.fail(w, 402, "plan_inactive", map[string]any{"paidUntil": nil})
			return
		}
		cfg, _ := in["config"].(map[string]any)
		if _, err := validateChannel(ChannelInput{Kind: str("kind"), Name: str("name"),
			BotToken: fmt.Sprint(cfg["botToken"]), ChatID: fmt.Sprint(cfg["chatId"]), WebhookURL: fmt.Sprint(cfg["webhookUrl"])}); err != nil {
			f.fail(w, 400, "invalid_config", nil)
			return
		}
		ch := Channel{ID: fmt.Sprintf("ch_%d", len(f.channels)+1), Kind: str("kind"), Name: str("name"), CreatedAt: f.clock().UTC().Format(time.RFC3339)}
		f.channels = append(f.channels, ch)
		f.ok(w, 201, map[string]string{"id": ch.ID, "kind": ch.Kind, "name": ch.Name})

	case r.Method == "DELETE" && strings.HasPrefix(path, "/v1/channels/"):
		if f.verify(w, r, body) == nil {
			return
		}
		id := strings.TrimPrefix(path, "/v1/channels/")
		for i, c := range f.channels {
			if c.ID == id {
				f.channels = append(f.channels[:i], f.channels[i+1:]...)
				w.WriteHeader(204)
				return
			}
		}
		f.fail(w, 404, "not_found", nil)

	case r.Method == "POST" && strings.HasSuffix(path, "/test") && strings.HasPrefix(path, "/v1/channels/"):
		if f.verify(w, r, body) == nil {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/channels/"), "/test")
		for i, c := range f.channels {
			if c.ID == id {
				f.channels[i].LastResult = &ChannelResult{OK: true, At: f.clock().UTC().Format(time.RFC3339)}
				f.ok(w, 200, TestResult{OK: true})
				return
			}
		}
		f.fail(w, 404, "not_found", nil)

	case r.Method == "POST" && path == "/v1/alerts":
		if f.verify(w, r, body) == nil {
			return
		}
		if !f.planActive {
			f.fail(w, 402, "plan_inactive", map[string]any{"paidUntil": f.paidUntil})
			return
		}
		if f.alertError != nil {
			if status, code := f.alertError(); status != 0 {
				ra := int64(120)
				if f.retryAfter != 0 {
					ra = f.retryAfter
				}
				f.fail(w, status, code, map[string]any{"retryAfter": ra})
				return
			}
		}
		var req struct {
			Lang   string  `json:"lang"`
			Events []Event `json:"events"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.Events) > 20 {
			f.fail(w, 400, "too_many_events", nil)
			return
		}
		f.batches = append(f.batches, req.Events)
		type result struct {
			EventID   string           `json:"eventId"`
			Delivered int              `json:"delivered"`
			Failed    []map[string]any `json:"failed"`
			Duplicate bool             `json:"duplicate"`
		}
		var res []result
		for _, e := range req.Events {
			n, failed := len(f.channels), false
			if f.alertResult != nil {
				n, failed = f.alertResult(e)
			}
			rr := result{EventID: e.ID, Delivered: n, Failed: []map[string]any{}}
			if failed {
				rr.Failed = append(rr.Failed, map[string]any{"channelId": "ch_1", "error": "HTTP 401"})
			}
			res = append(res, rr)
		}
		f.ok(w, 200, map[string]any{"results": res})

	default:
		f.fail(w, 404, "not_found", nil)
	}
}

func (f *fakeCloud) setPlan(active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planActive = active
	if active {
		f.paidUntil = "2026-11-03T00:00:00Z"
	}
}

func (f *fakeCloud) events() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Event
	for _, b := range f.batches {
		out = append(out, b...)
	}
	return out
}

func (f *fakeCloud) batchSizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for _, b := range f.batches {
		out = append(out, len(b))
	}
	return out
}

func (f *fakeCloud) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.log...)
}
