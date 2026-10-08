package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
)

const tgToken = "123456789:" + "AAHfiqksKZ8WmR2zSjiQ7_v4TMAKdiHm9T0"

type testEnv struct {
	f   *fakeCloud
	s   *Service
	sec *secret.Store
	dir string
}

func newEnv(t *testing.T, f *fakeCloud, machine string, mod ...func(*Config)) *testEnv {
	t.Helper()
	dir := t.TempDir()
	sec := secret.NewWithBackend(secret.NewMemory())
	return newEnvAt(t, f, dir, sec, machine, mod...)
}

func newEnvAt(t *testing.T, f *fakeCloud, dir string, sec *secret.Store, machine string, mod ...func(*Config)) *testEnv {
	t.Helper()
	cfg := Config{
		DataDir: dir, Secrets: sec, Version: "v0.3.0", DeviceName: "ACER-LAPTOP", Platform: "windows",
		BaseURL: f.srv.URL, HTTPClient: f.srv.Client(), Logf: t.Logf,
		SystemMachineID: func() (string, error) { return machine, nil },
		Host:            func(id string) (string, bool) { return "10.0.0." + id, true },
	}
	for _, m := range mod {
		m(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &testEnv{f: f, s: s, sec: sec, dir: dir}
}

func (e *testEnv) signIn(t *testing.T, email string) Status {
	t.Helper()
	ctx := context.Background()
	if err := e.s.SignInStart(ctx, email, "vi"); err != nil {
		t.Fatal(err)
	}
	st, err := e.s.SignInVerify(ctx, email, "123456")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (e *testEnv) run(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.s.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func fast(t *testing.T) {
	op, om, sr, rb := orderPollFirst, orderPollMax, sendRetryBase, retryBase
	orderPollFirst, orderPollMax, sendRetryBase, retryBase = 10*time.Millisecond, 30*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { orderPollFirst, orderPollMax, sendRetryBase, retryBase = op, om, sr, rb })
}

func asErr(t *testing.T, err error, code string) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want error %s, got %v (%#v)", code, err, err)
	}
	return e
}

func TestSignInRegistersDeviceAndSignsRequests(t *testing.T) {
	f := newFakeCloud(t)
	e := newEnv(t, f, "machine-A")
	st := e.signIn(t, " A@B.vn ")
	if !st.SignedIn || st.Email != "a@b.vn" || st.DeviceID != "dev_1" {
		t.Fatalf("status %+v", st)
	}
	if len(st.Devices) != 1 || !st.Devices[0].Current || st.Devices[0].Name != "ACER-LAPTOP" || st.Devices[0].AppVersion != "0.3.0" {
		t.Errorf("devices %+v", st.Devices)
	}
	if len(st.Prices) != 2 || st.Prices[0].Total != 286000 || st.Plan == nil || st.Plan.Active || !st.PlanInactive {
		t.Errorf("plan/prices %+v %+v", st.Plan, st.Prices)
	}
	if st.MachineSource != MachineSystem || f.devices[0].machine != Fingerprint("machine-A") || f.devices[0].platform != "windows" {
		t.Errorf("machine %s %s", st.MachineSource, f.devices[0].machine)
	}
	if f.signedOK == 0 {
		t.Fatal("GET /v1/me must have been signed and verified")
	}
	if st.Forwarding.Lang != "vi" {
		t.Errorf("forwarding language follows the sign-in language: %q", st.Forwarding.Lang)
	}

	// The key is in the secret store; the JSON state holds nothing secret.
	seed, err := e.sec.Get(e.s.keyName())
	if err != nil || seed == "" {
		t.Fatal("device key missing from the secret store")
	}
	raw, _ := os.ReadFile(e.s.statePath())
	if strings.Contains(string(raw), seed) || !strings.Contains(string(raw), `"deviceId": "dev_1"`) {
		t.Fatalf("state.json:\n%s", raw)
	}

	// A restart reads the cached state and signs with the stored key.
	e2 := newEnvAt(t, f, e.dir, e.sec, "machine-A")
	if !e2.s.Status().SignedIn {
		t.Fatal("not signed in after restart")
	}
	if _, err := e2.s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.signedOK; got < 2 {
		t.Fatalf("signed requests verified: %d", got)
	}
}

func TestSignInValidatesLocally(t *testing.T) {
	f := newFakeCloud(t)
	e := newEnv(t, f, "m")
	ctx := context.Background()
	if err := e.s.SignInStart(ctx, "nope", "en"); asErr(t, err, "invalid_email").Field != "email" {
		t.Error("field")
	}
	if _, err := e.s.SignInVerify(ctx, "a@b.vn", "12345"); asErr(t, err, "invalid_code").Field != "code" {
		t.Error("field")
	}
	if len(f.requests()) != 0 {
		t.Fatalf("invalid input must not reach the server: %v", f.requests())
	}
	if err := e.s.SignInStart(ctx, "a@b.vn", "en"); err != nil {
		t.Fatal(err)
	}
	_, err := e.s.SignInVerify(ctx, "a@b.vn", "654321")
	if ce := asErr(t, err, "invalid_code"); ce.Status != 400 {
		t.Errorf("status %d", ce.Status)
	}
}

func TestSignInFailsCleanlyWithoutSecretStore(t *testing.T) {
	f := newFakeCloud(t)
	e := newEnv(t, f, "m")
	e.s.sec = failingSecrets{secret.NewWithBackend(secret.NewMemory())}
	err := e.s.SignInStart(context.Background(), "a@b.vn", "en")
	asErr(t, err, "secret_store_unavailable")
	if len(f.requests()) != 0 {
		t.Fatal("no code may be sent when the key cannot be kept")
	}
}

func TestDeviceLimitAndReplace(t *testing.T) {
	f := newFakeCloud(t)
	var envs []*testEnv
	for i := range 3 {
		e := newEnv(t, f, fmt.Sprint("machine-", i))
		e.signIn(t, "a@b.vn")
		envs = append(envs, e)
	}
	fourth := newEnv(t, f, "machine-4")
	ctx := context.Background()
	if err := fourth.s.SignInStart(ctx, "a@b.vn", "en"); err != nil {
		t.Fatal(err)
	}
	_, err := fourth.s.SignInVerify(ctx, "a@b.vn", "123456")
	ce := asErr(t, err, "device_limit")
	if ce.Status != 409 || ce.Max != 3 || len(ce.Devices) != 3 || ce.ReplaceToken == "" {
		t.Fatalf("device_limit %+v", ce)
	}
	st, err := fourth.s.SignInReplace(ctx, ce.ReplaceToken, ce.Devices[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.SignedIn || st.DeviceID != "dev_4" || len(st.Devices) != 3 {
		t.Fatalf("after replace %+v", st)
	}
	// The replaced device finds out on its next request and signs out.
	_, err = envs[0].s.Refresh(ctx)
	asErr(t, err, "device_revoked")
	st0 := envs[0].s.Status()
	if st0.SignedIn || st0.SignedOutReason != ReasonRevoked {
		t.Fatalf("revoked device status %+v", st0)
	}
	if _, err := envs[0].sec.Get(envs[0].s.keyName()); err == nil {
		t.Error("key of a revoked device must be deleted")
	}
	// A replace token works once (and never while signed in).
	_, err = fourth.s.SignInReplace(ctx, ce.ReplaceToken, ce.Devices[1].ID)
	asErr(t, err, "already_signed_in")
	if _, err := fourth.s.SignOut(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = fourth.s.SignInReplace(ctx, ce.ReplaceToken, ce.Devices[1].ID)
	asErr(t, err, "invalid_replace_token")
}

func TestClockSkewIsLearnedOnce(t *testing.T) {
	f := newFakeCloud(t)
	e := newEnv(t, f, "m", func(c *Config) { c.Now = func() time.Time { return time.Now().Add(-2 * time.Hour) } })
	e.signIn(t, "a@b.vn") // verify is unsigned; the refresh after it hits clock_skew and retries
	reqs := f.requests()
	if n := strings.Count(strings.Join(reqs, "\n"), "GET /v1/me"); n != 2 {
		t.Fatalf("want one clock_skew retry, requests: %v", reqs)
	}
	if !e.s.Status().SignedIn || e.s.Status().Plan == nil {
		t.Fatal("refresh after the retry must succeed")
	}
	before := len(f.requests())
	if _, err := e.s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(f.requests()) - before; got != 1 {
		t.Fatalf("the offset must be remembered (%d requests)", got)
	}
}

func TestMismatchedMachineSignsOut(t *testing.T) {
	f := newFakeCloud(t)
	e := newEnv(t, f, "machine-A")
	e.signIn(t, "a@b.vn")
	// Same data dir and keychain, another machine (e.g. a copied profile).
	moved := newEnvAt(t, f, e.dir, e.sec, "machine-B")
	_, err := moved.s.Refresh(context.Background())
	asErr(t, err, "device_mismatch")
	if st := moved.s.Status(); st.SignedIn || st.SignedOutReason != ReasonMismatch {
		t.Fatalf("status %+v", st)
	}
}

func TestSignOutRevokesAndForgetsKey(t *testing.T) {
	f := newFakeCloud(t)
	e := newEnv(t, f, "m")
	e.signIn(t, "a@b.vn")
	revoked, err := e.s.SignOut(context.Background())
	if err != nil || !revoked {
		t.Fatalf("signout %v %v", revoked, err)
	}
	if !strings.Contains(strings.Join(f.requests(), "\n"), "DELETE /v1/devices/dev_1") {
		t.Fatalf("self revoke missing: %v", f.requests())
	}
	if !f.devices[0].revoked {
		t.Error("device not revoked on the server")
	}
	if _, err := e.sec.Get(e.s.keyName()); err == nil {
		t.Error("key must be deleted")
	}
	st := e.s.Status()
	if st.SignedIn || st.Email != "" || len(st.Devices) != 0 || len(st.Prices) == 0 {
		t.Errorf("status after sign-out %+v", st)
	}
	raw, _ := os.ReadFile(e.s.statePath())
	if strings.Contains(string(raw), "dev_1") || strings.Contains(string(raw), "a@b.vn") {
		t.Errorf("state.json keeps the account:\n%s", raw)
	}
	// Offline sign-out still forgets everything locally.
	e2 := newEnv(t, f, "m2")
	e2.signIn(t, "c@d.vn")
	f.srv.Close()
	revoked, _ = e2.s.SignOut(context.Background())
	if revoked || e2.s.Status().SignedIn {
		t.Error("offline sign-out: revoked must be false and the device signed out locally")
	}
}

func TestCheckoutPollsUntilPaid(t *testing.T) {
	fast(t)
	f := newFakeCloud(t)
	e := newEnv(t, f, "m")
	e.signIn(t, "a@b.vn")
	e.run(t)
	_, err := e.s.Checkout(context.Background(), 2)
	asErr(t, err, "invalid_months")
	o, err := e.s.Checkout(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if o.OrderCode != 175948000042 || !strings.HasPrefix(o.CheckoutURL, "https://pay.payos.vn/") || o.Amount != 858000 {
		t.Fatalf("order %+v", o)
	}
	st := e.s.Status()
	if st.Order == nil || !st.Order.Waiting || st.Order.Status != OrderPending {
		t.Fatalf("pending order %+v", st.Order)
	}
	time.Sleep(50 * time.Millisecond) // a few polls while pending
	f.mu.Lock()
	f.orders[o.OrderCode].Status = OrderPaid
	f.planActive, f.paidUntil = true, "2027-01-03T00:00:00Z"
	f.mu.Unlock()
	waitFor(t, "paid order", func() bool {
		st := e.s.Status()
		return st.Order != nil && st.Order.Status == OrderPaid && !st.Order.Waiting && st.Plan != nil && st.Plan.Active
	})
	if st := e.s.Status(); st.PlanInactive || st.Plan.PaidUntil != "2027-01-03T00:00:00Z" {
		t.Fatalf("plan after payment %+v", st.Plan)
	}
	if e.s.StopWaiting().Order != nil {
		t.Error("StopWaiting clears the order")
	}
}

func TestChannelsValidateLocallyAndNeverLeak(t *testing.T) {
	f := newFakeCloud(t)
	e := newEnv(t, f, "m")
	e.signIn(t, "a@b.vn")
	ctx := context.Background()
	before := len(f.requests())
	_, err := e.s.AddChannel(ctx, ChannelInput{Kind: "telegram", BotToken: "bad", ChatID: "1"})
	if asErr(t, err, "invalid_config").Field != "botToken" || len(f.requests()) != before {
		t.Fatal("local validation must stop the request")
	}
	_, err = e.s.AddChannel(ctx, ChannelInput{Kind: "telegram", BotToken: tgToken, ChatID: "-1001234567890"})
	asErr(t, err, "plan_inactive")
	if !e.s.Status().PlanInactive {
		t.Error("402 must mark the plan inactive")
	}

	f.setPlan(true)
	if _, err := e.s.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	ch, err := e.s.AddChannel(ctx, ChannelInput{Kind: "telegram", Name: "Ops", BotToken: tgToken, ChatID: "-1001234567890"})
	if err != nil || ch.ID != "ch_1" {
		t.Fatalf("add: %+v %v", ch, err)
	}
	st := e.s.Status()
	if len(st.Channels) != 1 || st.Channels[0].Name != "Ops" || st.PlanInactive {
		t.Fatalf("channels %+v", st.Channels)
	}
	res, err := e.s.TestChannel(ctx, ch.ID, "vi")
	if err != nil || !res.OK {
		t.Fatalf("test %+v %v", res, err)
	}
	if lr := e.s.Status().Channels[0].LastResult; lr == nil || !lr.OK {
		t.Error("test result not refreshed")
	}
	b, _ := json.Marshal(e.s.Status())
	raw, _ := os.ReadFile(e.s.statePath())
	for _, secretPart := range []string{tgToken, "AAHfiqks"} {
		if strings.Contains(string(b), secretPart) || strings.Contains(string(raw), secretPart) {
			t.Fatalf("channel secret leaked: %s / %s", b, raw)
		}
	}
	if _, err := e.s.TestChannel(ctx, "../me", "vi"); err == nil {
		t.Fatal("ids are validated before they reach a path")
	}
	if err := e.s.DeleteChannel(ctx, ch.ID); err != nil || len(e.s.Status().Channels) != 0 {
		t.Fatalf("delete %v", err)
	}
}

func crit(id, host string) health.Alert {
	return health.Alert{ID: id, HostID: host, HostName: "srv-" + host, To: health.LevelCrit, From: health.LevelOK,
		At: time.Now().UTC(), Findings: []health.Finding{{Code: "cpu", Level: health.LevelCrit, Value: 99}}}
}

// readyToForward signs in with an active plan and one channel.
func readyToForward(t *testing.T) *testEnv {
	t.Helper()
	return readyToForwardWith(t, newFakeCloud(t))
}

func readyToForwardWith(t *testing.T, f *fakeCloud) *testEnv {
	t.Helper()
	f.setPlan(true)
	e := newEnv(t, f, "m")
	e.signIn(t, "a@b.vn")
	if _, err := e.s.AddChannel(context.Background(), ChannelInput{Kind: "telegram", BotToken: tgToken, ChatID: "1"}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestAlertsAreBatchedAndFiltered(t *testing.T) {
	fast(t)
	e := readyToForward(t)
	for i := range 25 {
		e.s.accept(crit(fmt.Sprint("a", i), "1"))
	}
	e.run(t)
	waitFor(t, "25 events", func() bool { return len(e.f.events()) == 25 })
	for _, n := range e.f.batchSizes() {
		if n > batchMax {
			t.Fatalf("batch of %d", n)
		}
	}
	if e.f.batchSizes()[0] != 20 {
		t.Errorf("first batch %v", e.f.batchSizes())
	}
	got := e.f.events()[0]
	if got.ID != "tw-a0" || got.Level != "crit" || got.Kind != "health" || got.Host.Address != "10.0.0.1" ||
		got.Title.VI != "srv-1 đang nghiêm trọng" || got.Body.EN != "CPU 99%" {
		t.Errorf("event %+v", got)
	}
	waitFor(t, "empty queue", func() bool { return e.s.Status().Queue.Pending == 0 })
	if e.s.Status().Queue.LastSentAt == nil {
		t.Error("lastSentAt")
	}

	// Only critical problems from now on.
	e.s.SetForwarding(Forwarding{Critical: true, Lang: "en"})
	warn := crit("w1", "1")
	warn.To = health.LevelWarn
	rec := crit("r1", "1")
	rec.To = health.LevelOK
	e.s.Enqueue(warn)
	e.s.Enqueue(rec)
	e.s.Enqueue(crit("c1", "2"))
	waitFor(t, "critical event", func() bool { return len(e.f.events()) == 26 })
	time.Sleep(50 * time.Millisecond)
	evs := e.f.events()
	if len(evs) != 26 || evs[25].ID != "tw-c1" {
		t.Fatalf("filters let through: %+v", evs[25:])
	}
}

func TestDeliveryRefreshesUsage(t *testing.T) {
	fast(t)
	gap := sentRefreshGap
	sentRefreshGap = 0
	t.Cleanup(func() { sentRefreshGap = gap })
	e := readyToForward(t)
	e.run(t)
	countMe := func() int {
		n := 0
		for _, r := range e.f.requests() {
			if r == "GET /v1/me" {
				n++
			}
		}
		return n
	}
	waitFor(t, "start-up refresh", func() bool { return countMe() >= 1 })
	before := countMe()
	e.s.Enqueue(crit("u1", "1"))
	waitFor(t, "event", func() bool { return len(e.f.events()) == 1 })
	waitFor(t, "refresh after delivery", func() bool { return countMe() > before })
}

func TestEnqueueNeverBlocks(t *testing.T) {
	f := newFakeCloud(t)
	e := newEnv(t, f, "m")
	// Run is not started, so nothing drains the inbox.
	done := make(chan struct{})
	go func() {
		for i := range alertInbox * 3 {
			e.s.Enqueue(crit(fmt.Sprint(i), "1"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue blocked")
	}
}

func TestFailedEventsAreRetriedThenDelivered(t *testing.T) {
	fast(t)
	e := readyToForward(t)
	var calls atomic.Int32
	e.f.mu.Lock()
	e.f.alertResult = func(ev Event) (int, bool) {
		if calls.Add(1) == 1 {
			return 0, true // every channel failed: the server lets the client retry
		}
		return 1, false
	}
	e.f.mu.Unlock()
	e.run(t)
	e.s.Enqueue(crit("x", "1"))
	waitFor(t, "retry", func() bool { return len(e.f.events()) == 2 })
	waitFor(t, "delivered", func() bool { return e.s.Status().Queue.Pending == 0 })
	evs := e.f.events()
	if evs[0].ID != evs[1].ID {
		t.Fatal("a retry must reuse the event id so the server can dedupe")
	}
}

func TestPlanInactiveStopsSendingUntilRenewed(t *testing.T) {
	fast(t)
	e := readyToForward(t)
	e.run(t)
	e.f.setPlan(false)
	e.s.Enqueue(crit("p1", "1"))
	waitFor(t, "plan inactive", func() bool { return e.s.Status().PlanInactive })
	if e.s.Status().Queue.Pending != 1 {
		t.Fatal("the refused event stays queued")
	}
	posts := func() int { return strings.Count(strings.Join(e.f.requests(), "\n"), "POST /v1/alerts") }
	n := posts()
	e.s.Enqueue(crit("p2", "1")) // not even queued while inactive
	time.Sleep(100 * time.Millisecond)
	if posts() != n || e.s.Status().Queue.Pending != 1 {
		t.Fatalf("sending must stop while the plan is inactive (%d posts, %d pending)", posts()-n, e.s.Status().Queue.Pending)
	}
	e.f.setPlan(true)
	if _, err := e.s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "resume after renewal", func() bool { return e.s.Status().Queue.Pending == 0 })
	if evs := e.f.events(); len(evs) != 1 || evs[0].ID != "tw-p1" {
		t.Fatalf("events %+v", evs)
	}
}

func TestQueueSurvivesRestart(t *testing.T) {
	fast(t)
	e := readyToForward(t)
	e.s.accept(crit("q1", "1")) // queued and saved, nothing running yet
	e.s.accept(crit("q2", "1"))
	if e.s.Status().Queue.Pending != 2 {
		t.Fatal("not queued")
	}
	e2 := newEnvAt(t, e.f, e.dir, e.sec, "m")
	if e2.s.Status().Queue.Pending != 2 {
		t.Fatal("queue lost on restart")
	}
	e2.run(t)
	waitFor(t, "sent after restart", func() bool { return len(e.f.events()) == 2 })
}

func TestServerErrorsBackOffAndRateLimitWaits(t *testing.T) {
	fast(t)
	e := readyToForward(t)
	var fail atomic.Int32
	fail.Store(503)
	e.f.mu.Lock()
	e.f.alertError = func() (int, string) {
		switch fail.Load() {
		case 503:
			return 503, "internal"
		case 429:
			return 429, "rate_limited"
		}
		return 0, ""
	}
	e.f.mu.Unlock()

	e.run(t)
	e.s.Enqueue(crit("s1", "1"))
	waitFor(t, "backoff", func() bool { st := e.s.Status(); return st.Queue.LastError == "internal" && st.Queue.RetryAt != nil })

	fail.Store(429)
	waitFor(t, "rate limited", func() bool { return e.s.Status().Queue.LastError == "rate_limited" })
	st := e.s.Status()
	if st.Queue.RetryAt == nil || time.Until(*st.Queue.RetryAt) < 100*time.Second {
		t.Fatalf("429 must wait for retryAfter (120 s): %+v", st.Queue)
	}
	if st.Queue.Pending != 1 {
		t.Fatal("event must stay queued")
	}
}

// A retryAfter too large for time.Duration must not wrap into a negative wait
// (the send loop then retries in a tight loop against the server).
func TestHugeRetryAfterDoesNotBusyLoop(t *testing.T) {
	fast(t)
	e := readyToForward(t)
	e.f.mu.Lock()
	e.f.alertError = func() (int, string) { return 429, "rate_limited" }
	e.f.retryAfter = 10_000_000_000 // seconds: × time.Second wraps to a negative Duration
	e.f.mu.Unlock()

	e.run(t)
	e.s.Enqueue(crit("h1", "1"))
	waitFor(t, "rate limited", func() bool { return e.s.Status().Queue.LastError == "rate_limited" })
	time.Sleep(300 * time.Millisecond)
	if n := strings.Count(strings.Join(e.f.requests(), "\n"), "POST /v1/alerts"); n != 1 {
		t.Fatalf("a 429 must stop sending until retryAfter; sent %d requests", n)
	}
	st := e.s.Status()
	if st.Queue.RetryAt == nil || time.Until(*st.Queue.RetryAt) > 25*time.Hour || time.Until(*st.Queue.RetryAt) < time.Hour {
		t.Fatalf("retryAt must be capped to a day: %+v", st.Queue)
	}
}

// Replacing a device while signed in would move this install (its key and
// its queued alerts) to whatever account the replace token belongs to.
func TestReplaceRefusedWhileSignedIn(t *testing.T) {
	f := newFakeCloud(t)
	other := newEnv(t, f, "machine-B")
	other.signIn(t, "b@b.vn")
	f.mu.Lock()
	f.tokens["tok_b"] = "b@b.vn"
	f.mu.Unlock()

	e := readyToForwardWith(t, f)
	e.s.accept(crit("q1", "1"))
	_, err := e.s.SignInReplace(context.Background(), "tok_b", other.s.Status().DeviceID)
	asErr(t, err, "already_signed_in")
	if st := e.s.Status(); st.Email != "a@b.vn" || st.Queue.Pending != 1 {
		t.Fatalf("status changed: %+v", st)
	}
	if _, err := other.s.Refresh(context.Background()); err != nil {
		t.Fatalf("the other account's device must not be revoked: %v", err)
	}
}

func TestDeviceRevokedWhileForwarding(t *testing.T) {
	fast(t)
	e := readyToForward(t)
	e.f.mu.Lock()
	e.f.devices[0].revoked = true // removed from another device
	e.f.mu.Unlock()
	e.run(t)
	e.s.Enqueue(crit("r", "1"))
	waitFor(t, "signed out", func() bool { return !e.s.Status().SignedIn })
	st := e.s.Status()
	if st.SignedOutReason != ReasonRevoked || st.Queue.Pending != 0 {
		t.Fatalf("status %+v", st)
	}
}

func TestErrorAnswersAreParsed(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plain429":
			w.Header().Set("Retry-After", "42")
			w.WriteHeader(429)
			_, _ = w.Write([]byte("slow down"))
		case "/json402":
			w.WriteHeader(402)
			_, _ = w.Write([]byte(`{"error":"plan_inactive","message":"Termward Pro is not active","paidUntil":"2026-01-01T00:00:00Z","unknown":1}`))
		case "/redirect":
			http.Redirect(w, r, "https://example.com/", http.StatusFound)
		case "/garbage":
			_, _ = w.Write([]byte("<html>"))
		}
	}))
	defer ts.Close()
	c := &client{base: ts.URL, http: newHTTPClient(), now: time.Now}
	c.http.Transport = ts.Client().Transport // trust the test certificate, verification stays on
	ctx := context.Background()

	err := c.do(ctx, "GET", "/plain429", nil, nil, nil, 0)
	if e := asErr(t, err, "rate_limited"); e.Status != 429 || e.RetryAfter != 42 {
		t.Errorf("%+v", e)
	}
	err = c.do(ctx, "GET", "/json402", nil, nil, nil, 0)
	if e := asErr(t, err, "plan_inactive"); e.Status != 402 || e.PaidUntil != "2026-01-01T00:00:00Z" {
		t.Errorf("%+v", e)
	}
	err = c.do(ctx, "GET", "/redirect", nil, nil, nil, 0)
	if e := asErr(t, err, "error"); e.Status != http.StatusFound {
		t.Errorf("redirects must not be followed: %+v", e)
	}
	var out map[string]any
	err = c.do(ctx, "GET", "/garbage", nil, nil, &out, 0)
	asErr(t, err, "bad_response")

	// Without the test CA the certificate is rejected: TLS verification is on.
	strict := &client{base: ts.URL, http: newHTTPClient(), now: time.Now}
	err = strict.do(ctx, "GET", "/json402", nil, nil, nil, 0)
	asErr(t, err, "cloud_unreachable")
	if !retryable(err) || retryable(&Error{Status: 402, Code: "plan_inactive"}) {
		t.Error("retryable")
	}
}

func TestInstallsDoNotShareKeys(t *testing.T) {
	f := newFakeCloud(t)
	sec := secret.NewWithBackend(secret.NewMemory()) // one keychain, two data dirs
	a := newEnvAt(t, f, t.TempDir(), sec, "m")
	b := newEnvAt(t, f, t.TempDir(), sec, "m")
	if a.s.keyName() == b.s.keyName() {
		t.Fatal("two installs must use different key names")
	}
	a.signIn(t, "a@b.vn")
	b.signIn(t, "c@d.vn")
	if _, err := b.s.SignOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.s.Refresh(context.Background()); err != nil {
		t.Fatalf("signing out one install must not break the other: %v", err)
	}
	// The install id survives sign-out and restarts.
	again := newEnvAt(t, f, a.dir, sec, "m")
	if again.s.keyName() != a.s.keyName() {
		t.Fatal("install id not persisted")
	}
}
