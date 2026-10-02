package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nguyenquocanhz/diagward/model"

	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// ---------------------------------------------------------------- diffing

func finding(id, target, comp string, sev model.Severity) model.Finding {
	return model.Finding{ID: id, Target: target, Component: comp, Severity: sev,
		Title: model.T("EN "+id, "VI "+id)}
}

func result(ranAs string, partial bool, fs ...model.Finding) *HardwareResult {
	r := &model.Report{Findings: fs, Verdict: model.OK}
	for _, f := range fs {
		r.Verdict = model.Worst(r.Verdict, f.Severity)
	}
	for _, c := range []string{"disk", "memory", "power"} {
		r.Summary = append(r.Summary, model.ComponentSummary{Component: c, Checked: true})
	}
	r.Summary = append(r.Summary, model.ComponentSummary{Component: "bmc", Checked: false})
	return &HardwareResult{Report: r, RanAs: ranAs, Partial: partial, SavedAt: time.Now().UTC()}
}

func changes(cs []health.HardwareChange) string {
	var out []string
	for _, c := range cs {
		s := c.Change + ":" + c.ID
		if c.Target != "" {
			s += "@" + c.Target
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

func TestDiffHardware(t *testing.T) {
	smart := func(sev model.Severity) model.Finding { return finding("disk.smart", "/dev/sda", "disk", sev) }
	smartB := func(sev model.Severity) model.Finding { return finding("disk.smart", "/dev/sdb", "disk", sev) }
	ecc := func(sev model.Severity) model.Finding { return finding("memory.ecc", "DIMM A1", "memory", sev) }
	bmc := func(sev model.Severity) model.Finding { return finding("bmc.sel", "", "bmc", sev) }
	psu := func(sev model.Severity) model.Finding { return finding("power.psu", "PSU 2", "power", sev) }

	for _, c := range []struct {
		name      string
		prev, cur *HardwareResult
		want      string
	}{
		{"first check, only warnings: quiet", nil, result("root", false, smart(model.Warn), ecc(model.Info)), ""},
		{"first check with a crit", nil, result("root", false, smart(model.Warn), psu(model.Crit)), "new:power.psu@PSU 2"},
		{"unchanged", result("root", false, smart(model.Warn)), result("root", false, smart(model.Warn)), ""},
		{"new warning on another disk (target matters)",
			result("root", false, smart(model.Warn)), result("sudo", false, smart(model.Warn), smartB(model.Warn)),
			"new:disk.smart@/dev/sdb"},
		{"info to warn is new", result("root", false, ecc(model.Info)), result("root", false, ecc(model.Warn)), "new:memory.ecc@DIMM A1"},
		{"warn to crit is worse", result("root", false, smart(model.Warn)), result("root", false, smart(model.Crit)), "worse:disk.smart@/dev/sda"},
		{"crit gone is resolved", result("root", false, psu(model.Crit), smart(model.Warn)), result("root", false, smart(model.Warn)),
			"resolved:power.psu@PSU 2"},
		{"crit to info is resolved", result("root", false, psu(model.Crit)), result("root", false, psu(model.Info)), "resolved:power.psu@PSU 2"},
		{"crit to warn is neither", result("root", false, psu(model.Crit)), result("root", false, psu(model.Warn)), ""},
		{"warn gone: no recovery alert", result("root", false, smart(model.Warn)), result("root", false), ""},
		{"partial check does not resolve", result("root", false, psu(model.Crit)), result("root", true), ""},
		{"unchecked component does not resolve", result("root", false, bmc(model.Crit)), result("root", false), ""},
		{"check without root does not resolve", result("root", false, psu(model.Crit)), result("user", false), ""},
		{"but a new problem without root is still new",
			result("root", false), result("user", false, ecc(model.Warn)), "new:memory.ecc@DIMM A1"},
		{"root after a no-root check: only new crits",
			result("user", false, ecc(model.Warn)), result("root", false, ecc(model.Warn), smart(model.Warn), psu(model.Crit)),
			"new:power.psu@PSU 2"},
		{"duplicates keep the worst",
			result("root", false, smart(model.Warn)), result("root", false, smart(model.Warn), smart(model.Crit)),
			"worse:disk.smart@/dev/sda"},
	} {
		if got := changes(diffHardware(c.prev, c.cur)); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestHardwareAlertsText(t *testing.T) {
	prev := result("root", false, finding("power.psu", "PSU 1", "power", model.Crit))
	cur := result("root", false,
		finding("disk.smart", "/dev/sda", "disk", model.Warn),
		finding("memory.ecc", "DIMM A1", "memory", model.Crit))
	alerts := hardwareAlerts("h1", "db-01", prev, diffHardware(prev, cur))
	if len(alerts) != 2 {
		t.Fatalf("want a problem and a recovery alert, got %+v", alerts)
	}
	p, r := alerts[0], alerts[1]
	if p.Kind != "hardware" || p.To != health.LevelCrit || p.From != health.LevelCrit || len(p.Hardware) != 2 {
		t.Fatalf("problem alert: %+v", p)
	}
	if p.Hardware[0].ID != "memory.ecc" {
		t.Errorf("the crit change must come first: %+v", p.Hardware)
	}
	if ti, b := health.AlertText(p, "vi"); ti != "db-01 có lỗi phần cứng nghiêm trọng" || b != "VI memory.ecc (DIMM A1) · và 1 mục khác" {
		t.Errorf("vi: %q / %q", ti, b)
	}
	if ti, b := health.AlertText(p, "en"); ti != "db-01 has a critical hardware problem" || b != "EN memory.ecc (DIMM A1) · 1 more" {
		t.Errorf("en: %q / %q", ti, b)
	}
	if r.To != health.LevelOK || r.Hardware[0].Change != changeResolved || r.Hardware[0].To != "ok" {
		t.Fatalf("recovery alert: %+v", r)
	}
	if ti, _ := health.AlertText(r, "en"); ti != "db-01: hardware problem resolved" {
		t.Errorf("recovery title: %q", ti)
	}

	warnOnly := hardwareAlerts("h1", "db-01", nil, []health.HardwareChange{{Change: changeNew, ID: "x", To: "warn", Title: health.Text{EN: "A", VI: "B"}}})
	if len(warnOnly) != 1 || warnOnly[0].To != health.LevelWarn || warnOnly[0].From != health.LevelUnknown {
		t.Fatalf("warn alert: %+v", warnOnly)
	}
	if ti, _ := health.AlertText(warnOnly[0], "vi"); ti != "db-01 có cảnh báo phần cứng" {
		t.Errorf("warn title: %q", ti)
	}
}

func TestSummarize(t *testing.T) {
	res := result("sudo", false, finding("a", "", "disk", model.Info), finding("b", "", "disk", model.Warn), finding("c", "", "power", model.Crit))
	s := summarize(res)
	if s.Headline != "crit" || s.Crit != 1 || s.Warn != 1 || s.Info != 1 || s.Top == nil || s.Top.EN != "EN c" {
		t.Fatalf("summary: %+v", s)
	}
	guest := result("root", false)
	guest.Report.Env = model.Env{OS: "linux", Virtual: "kvm"}
	if s := summarize(guest); s.Headline != "guest" || s.Top != nil {
		t.Fatalf("guest: %+v", s)
	}
	none := &HardwareResult{Report: &model.Report{}}
	if s := summarize(none); s.Headline != "none" {
		t.Fatalf("none: %+v", s)
	}
}

// ---------------------------------------------------------------- scheduler

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type schedEnv struct {
	t     *testing.T
	dir   string
	st    *store.Store
	srv   *Server
	hs    *hwScheduler
	clock *fakeClock
	hosts []store.Host
}

// newSchedEnv makes a core with n monitored hosts and no SSH at all: checks
// are replaced by the caller.
func newSchedEnv(t *testing.T, dir string, n int, interval string) *schedEnv {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	hosts := st.Hosts()
	for i := len(hosts); i < n; i++ {
		h, err := st.SaveHost(store.Host{Name: "srv" + string(rune('a'+i)), Address: "10.0.0.1", User: "root", Auth: store.AuthAgent, Monitor: true})
		if err != nil {
			t.Fatal(err)
		}
		hosts = append(hosts, h)
	}
	set := st.Settings()
	set.HardwareInterval = interval
	if _, err := st.SaveSettings(set); err != nil {
		t.Fatal(err)
	}
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := New(ctx, Deps{Token: "secret-token", Store: st, Monitor: health.NewMonitor(st, nil, hub.Publish), Hub: hub})
	clock := &fakeClock{t: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)}
	srv.hw.now = clock.now
	srv.hw.randDur = func(d time.Duration) time.Duration { return d / 2 }
	return &schedEnv{t: t, dir: dir, st: st, srv: srv, hs: srv.hw, clock: clock, hosts: hosts}
}

// succeed records a healthy result, like a real check would.
func (e *schedEnv) succeed(id string, src hwSource) {
	res := result("root", false)
	res.SavedAt = e.clock.now()
	e.hs.recorded(id, res, src)
}

func (e *schedEnv) plan(id string) hwPlan {
	e.hs.mu.Lock()
	defer e.hs.mu.Unlock()
	return *e.hs.planLocked(id)
}

func TestSchedulerSpreadsLimitsAndPersists(t *testing.T) {
	dir := t.TempDir()
	e := newSchedEnv(t, dir, 5, store.HardwareDaily)
	var running, peak, total atomic.Int32
	perHost := map[string]int{}
	var mu sync.Mutex
	release := make(chan struct{})
	e.hs.check = func(ctx context.Context, id string, src hwSource) error {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		mu.Lock()
		perHost[id]++
		mu.Unlock()
		<-release
		running.Add(-1)
		total.Add(1)
		if src != srcSchedule {
			t.Errorf("source %q", src)
		}
		e.succeed(id, src)
		return nil
	}
	ctx := context.Background()

	// First sight: every host gets a next run a little later, none now.
	e.hs.tick(ctx)
	t0 := e.clock.now()
	for _, h := range e.hosts {
		p := e.plan(h.ID)
		// 2 min pause + half (fake random) of min(24h/4, 1h)
		if want := t0.Add(2*time.Minute + 30*time.Minute); !p.NextRun.Equal(want) {
			t.Fatalf("next run %v, want %v", p.NextRun, want)
		}
	}
	if total.Load() != 0 {
		t.Fatal("nothing is due yet")
	}

	// Due: all five start, but only two at a time, once each.
	e.clock.add(33 * time.Minute)
	e.hs.tick(ctx)
	e.hs.tick(ctx) // ticking again while they run must not queue them twice
	deadline := time.After(5 * time.Second)
	for running.Load() < hwMaxConcurrent {
		select {
		case <-deadline:
			t.Fatal("checks did not start")
		case <-time.After(5 * time.Millisecond):
		}
	}
	time.Sleep(50 * time.Millisecond)
	if running.Load() != hwMaxConcurrent {
		t.Fatalf("%d checks running at once", running.Load())
	}
	if v := e.hs.fleet(); countState(v, "running") != 2 || countState(v, "queued") != 3 {
		t.Fatalf("fleet states: %+v", v.Hosts)
	}
	close(release)
	e.hs.jobs.Wait()
	if total.Load() != 5 || peak.Load() != hwMaxConcurrent {
		t.Fatalf("total %d, peak %d", total.Load(), peak.Load())
	}
	for id, n := range perHost {
		if n != 1 {
			t.Errorf("host %s checked %d times", id, n)
		}
	}
	done := e.clock.now()
	for _, h := range e.hosts {
		// one period + half of min(24h/10, 30 min)
		if p := e.plan(h.ID); !p.NextRun.Equal(done.Add(24*time.Hour+15*time.Minute)) || p.Error != "" {
			t.Fatalf("after the check: %+v", p)
		}
	}

	// The plan survives a restart.
	raw, err := os.ReadFile(filepath.Join(dir, hwScheduleFile))
	if err != nil || !strings.Contains(string(raw), e.hosts[0].ID) {
		t.Fatalf("schedule not saved: %v %s", err, raw)
	}
	e2 := newSchedEnv(t, dir, 5, store.HardwareDaily)
	e2.clock.t = done.Add(time.Hour)
	var ran atomic.Int32
	e2.hs.check = func(context.Context, string, hwSource) error { ran.Add(1); return nil }
	e2.hs.tick(ctx)
	e2.hs.jobs.Wait()
	if ran.Load() != 0 {
		t.Fatal("a restart must not re-run checks that are not due")
	}
	if p := e2.plan(e.hosts[0].ID); !p.NextRun.Equal(done.Add(24*time.Hour + 15*time.Minute)) {
		t.Fatalf("next run lost on restart: %v", p.NextRun)
	}

	// Down for three days: the overdue hosts are spread out again, not run
	// all at once the moment Termward starts.
	e3 := newSchedEnv(t, dir, 5, store.HardwareDaily)
	e3.clock.t = done.Add(72 * time.Hour)
	e3.hs.check = func(context.Context, string, hwSource) error { ran.Add(1); return nil }
	e3.hs.tick(ctx)
	e3.hs.jobs.Wait()
	if ran.Load() != 0 {
		t.Fatal("overdue checks must not burst on start")
	}
	if p := e3.plan(e.hosts[0].ID); !p.NextRun.After(e3.clock.now()) {
		t.Fatalf("overdue host not rescheduled: %v", p.NextRun)
	}
}

func countState(v fleetView, state string) int {
	n := 0
	for _, h := range v.Hosts {
		if h.State == state {
			n++
		}
	}
	return n
}

func TestSchedulerSudoRequiredAndOff(t *testing.T) {
	e := newSchedEnv(t, t.TempDir(), 2, store.HardwareWeekly)
	needSudo := e.hosts[0].ID
	var calls atomic.Int32
	e.hs.check = func(_ context.Context, id string, src hwSource) error {
		calls.Add(1)
		if id == needSudo {
			return &sudoError{}
		}
		e.succeed(id, src)
		return nil
	}
	ctx := context.Background()
	e.hs.tick(ctx)
	e.clock.add(3 * time.Hour)
	e.hs.tick(ctx)
	e.hs.jobs.Wait()
	if calls.Load() != 2 {
		t.Fatalf("calls %d", calls.Load())
	}
	p := e.plan(needSudo)
	if p.Error != "sudo_required" || !p.NextRun.After(e.clock.now().Add(6*24*time.Hour)) {
		t.Fatalf("a host that needs a sudo password waits for the next period: %+v", p)
	}
	var view HardwareHost
	for _, h := range e.hs.fleet().Hosts {
		if h.HostID == needSudo {
			view = h
		}
	}
	if view.Error != "sudo_required" || view.Summary != nil || view.NextRun == nil {
		t.Fatalf("fleet view: %+v", view)
	}
	// Not retried an hour later.
	e.clock.add(time.Hour)
	e.hs.tick(ctx)
	e.hs.jobs.Wait()
	if calls.Load() != 2 {
		t.Fatal("sudo_required was retried too soon")
	}
	// A successful check by hand clears the problem.
	e.succeed(needSudo, srcManual)
	if p := e.plan(needSudo); p.Error != "" {
		t.Fatalf("manual success should clear the error: %+v", p)
	}

	// Off: plans are cleared and nothing runs.
	set := e.st.Settings()
	set.HardwareInterval = store.HardwareOff
	e.st.SaveSettings(set)
	e.clock.add(30 * 24 * time.Hour)
	e.hs.tick(ctx)
	e.hs.jobs.Wait()
	if calls.Load() != 2 || !e.plan(needSudo).NextRun.IsZero() {
		t.Fatalf("scheduling off still ran checks or kept plans: %d %+v", calls.Load(), e.plan(needSudo))
	}
	for _, h := range e.hs.fleet().Hosts {
		if h.NextRun != nil {
			t.Fatalf("next run shown while off: %+v", h)
		}
	}
}

func TestSchedulerSkipsUnmonitoredAndShortensPeriod(t *testing.T) {
	e := newSchedEnv(t, t.TempDir(), 2, store.HardwareWeekly)
	h := e.hosts[1]
	h.Monitor = false
	if _, err := e.st.SaveHost(h); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e.succeed(e.hosts[0].ID, srcManual) // checked by hand before scheduling started
	e.hs.tick(ctx)
	if p := e.plan(h.ID); !p.NextRun.IsZero() {
		t.Fatalf("unmonitored host scheduled: %+v", p)
	}
	week := e.plan(e.hosts[0].ID).NextRun
	if week.Before(e.clock.now().Add(7 * 24 * time.Hour)) {
		t.Fatalf("a host checked by hand is next due one period later, got %v", week)
	}
	set := e.st.Settings()
	set.HardwareInterval = store.HardwareDaily
	e.st.SaveSettings(set)
	e.hs.tick(ctx)
	if p := e.plan(e.hosts[0].ID); !p.NextRun.Before(e.clock.now().Add(2 * time.Hour)) {
		t.Fatalf("switching to daily should bring the next run closer: %v", p.NextRun)
	}
}

func TestSchedulerSkipsHostWithManualCheck(t *testing.T) {
	e := newSchedEnv(t, t.TempDir(), 1, store.HardwareDaily)
	id := e.hosts[0].ID
	// A manual check holds the host; the real unattended check must back off
	// without touching SSH (there is no pool in this core).
	e.srv.hwBusy.Store(id, srcManual)
	if v := e.hs.fleet(); v.Hosts[0].State != "manual" {
		t.Fatalf("state during a manual check: %+v", v.Hosts[0])
	}
	ctx := context.Background()
	e.hs.tick(ctx)
	e.clock.add(3 * time.Hour)
	e.hs.tick(ctx)
	e.hs.jobs.Wait()
	p := e.plan(id)
	if p.Error != "" || !p.NextRun.After(e.clock.now()) || p.NextRun.After(e.clock.now().Add(time.Hour)) {
		t.Fatalf("busy host should be retried soon, without an error: %+v", p)
	}
	// Fleet runs report it as skipped.
	run, err := e.hs.startRun(ctx, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	e.hs.jobs.Wait()
	run = e.hs.fleet().Run
	if run.Active || len(run.Skipped) != 1 || run.Done != 1 {
		t.Fatalf("fleet run with a busy host: %+v", run)
	}
}

func TestFleetRun(t *testing.T) {
	e := newSchedEnv(t, t.TempDir(), 4, store.HardwareOff)
	ids := []string{e.hosts[0].ID, e.hosts[1].ID, e.hosts[2].ID, e.hosts[3].ID}
	release := make(chan struct{})
	var running, peak atomic.Int32
	e.hs.check = func(_ context.Context, id string, src hwSource) error {
		n := running.Add(1)
		if n > peak.Load() {
			peak.Store(n)
		}
		<-release
		running.Add(-1)
		switch id {
		case ids[1]:
			return &sudoError{}
		case ids[2]:
			return errors.New("dial tcp: i/o timeout")
		}
		e.succeed(id, src)
		return nil
	}
	events, unsubscribe := e.srv.hub.Subscribe()
	defer unsubscribe()
	ctx := context.Background()
	run, err := e.hs.startRun(ctx, ids)
	if err != nil || !run.Active || len(run.Hosts) != 4 {
		t.Fatalf("start: %v %+v", err, run)
	}
	if _, err := e.hs.startRun(ctx, ids); !errors.Is(err, errRunActive) {
		t.Fatalf("a second run while one is active: %v", err)
	}
	close(release)
	e.hs.jobs.Wait()
	run = e.hs.fleet().Run
	if run.Active || run.FinishedAt == nil || run.Done != 4 || len(run.OK) != 2 ||
		strings.Join(run.NeedsSudo, ",") != ids[1] || run.Failed[ids[2]] != "connect_failed" {
		t.Fatalf("finished run: %+v", run)
	}
	if peak.Load() > hwMaxConcurrent {
		t.Fatalf("peak %d", peak.Load())
	}
	var sawRun, sawHost bool
	for len(events) > 0 {
		var m struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		json.Unmarshal(<-events, &m)
		sawRun = sawRun || m.Type == "hardware_run"
		sawHost = sawHost || m.Type == "hardware_host"
	}
	if !sawRun || !sawHost {
		t.Fatalf("events: run=%v host=%v", sawRun, sawHost)
	}
}

func TestFleetRunCancel(t *testing.T) {
	e := newSchedEnv(t, t.TempDir(), 4, store.HardwareOff)
	release := make(chan struct{})
	var started atomic.Int32
	e.hs.check = func(_ context.Context, id string, src hwSource) error {
		started.Add(1)
		<-release
		e.succeed(id, src)
		return nil
	}
	var ids []string
	for _, h := range e.hosts {
		ids = append(ids, h.ID)
	}
	if _, err := e.hs.startRun(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	for started.Load() < hwMaxConcurrent {
		time.Sleep(5 * time.Millisecond)
	}
	run := e.hs.cancelRun()
	if !run.Active || len(run.Skipped) != 2 || run.Done != 2 {
		t.Fatalf("after cancel: %+v", run)
	}
	close(release)
	e.hs.jobs.Wait()
	run = e.hs.fleet().Run
	if run.Active || len(run.OK) != 2 || len(run.Skipped) != 2 || started.Load() != 2 {
		t.Fatalf("cancelled run: %+v (started %d)", run, started.Load())
	}
}

// ---------------------------------------------------------------- API

func TestHardwareSettingAndFleetAPI(t *testing.T) {
	ts := newTestServer(t)
	const tok = "secret-token"
	res, body := call(t, ts, "GET", "/api/settings", tok, "")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"hardwareInterval":"off"`) {
		t.Fatalf("default: %d %s", res.StatusCode, body)
	}
	var set store.Settings
	json.Unmarshal([]byte(body), &set)
	for _, c := range []struct{ in, want string }{{"weekly", "weekly"}, {"daily", "daily"}, {"hourly", "off"}, {"", "off"}} {
		set.HardwareInterval = c.in
		b, _ := json.Marshal(set)
		res, body = call(t, ts, "PUT", "/api/settings", tok, string(b))
		if res.StatusCode != http.StatusOK || !strings.Contains(body, `"hardwareInterval":"`+c.want+`"`) {
			t.Fatalf("set %q: %d %s", c.in, res.StatusCode, body)
		}
		_, body = call(t, ts, "GET", "/api/hardware", tok, "")
		if !strings.Contains(body, `"interval":"`+c.want+`"`) {
			t.Fatalf("fleet after %q: %s", c.in, body)
		}
	}
	if res, body := call(t, ts, "POST", "/api/hardware/run-all", tok, ""); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("run-all without servers: %d %s", res.StatusCode, body)
	}
	if res, body := call(t, ts, "POST", "/api/hardware/run-all", tok, `{"hostIds":["nope"]}`); res.StatusCode != http.StatusNotFound {
		t.Fatalf("run-all unknown host: %d %s", res.StatusCode, body)
	}
	if res, _ := call(t, ts, "POST", "/api/hardware/run-all/cancel", tok, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("cancel without a run: %d", res.StatusCode)
	}
	if res, _ := call(t, ts, "GET", "/api/hardware", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("fleet needs the token: %d", res.StatusCode)
	}
}

// diskHandler is a root Linux server whose / is 40% or 99% full.
func diskHandler(t *testing.T, full *atomic.Bool) execHandler {
	base := linuxHandler(t, true)
	return func(cmd string, stdin []byte) (string, int) {
		out, code := base(cmd, stdin)
		m := boundaryRe.FindStringSubmatch(string(stdin))
		if cmd != "sh -s" || m == nil {
			return out, code
		}
		b := m[1]
		used, avail, pct := "40000000000", "60000000000", "40%"
		if full.Load() {
			used, avail, pct = "99000000000", "1000000000", "99%"
		}
		df := "==DW:" + b + ":BEGIN filesystem.df\n" +
			"Filesystem     Type 1-blocks Used Available Capacity Mounted on\n" +
			"/dev/sda1 ext4 100000000000 " + used + " " + avail + " " + pct + " /\n" +
			"==DW:" + b + ":ERR\n\n==DW:" + b + ":END rc=0 ms=3\n"
		return strings.Replace(out, "==DW:"+b+":BEGIN meta.done", df+"==DW:"+b+":BEGIN meta.done", 1), code
	}
}

// TestScheduledCheckEndToEnd runs a scheduled check against the fake SSH
// server, then lets the disk fill up and checks that the next check raises a
// hardware alert through the health alert pipeline, and a recovery after.
func TestScheduledCheckEndToEnd(t *testing.T) {
	var full atomic.Bool
	e := newHWEnv(t, diskHandler(t, &full))
	h := e.host
	h.Monitor = true
	if _, err := e.srv.store.SaveHost(h); err != nil {
		t.Fatal(err)
	}
	set := e.srv.store.Settings()
	set.HardwareInterval = store.HardwareDaily
	e.srv.store.SaveSettings(set)
	clock := &fakeClock{t: time.Now()}
	e.srv.hw.now = clock.now
	ctx := context.Background()

	e.srv.hw.tick(ctx)
	clock.add(2 * time.Hour)
	e.srv.hw.tick(ctx)
	e.srv.hw.jobs.Wait()

	res, body := e.call("GET", "/api/hardware", "")
	var fleet fleetView
	json.Unmarshal([]byte(body), &fleet)
	if res.StatusCode != http.StatusOK || len(fleet.Hosts) != 1 || fleet.Hosts[0].Summary == nil || fleet.Hosts[0].Error != "" {
		t.Fatalf("after the scheduled check: %d %s", res.StatusCode, body)
	}
	if fleet.Hosts[0].Summary.RanAs != "root" || fleet.Hosts[0].NextRun == nil {
		t.Fatalf("summary: %+v", fleet.Hosts[0])
	}
	if n := len(e.srv.monitor.Alerts()); n != 0 {
		t.Fatalf("a healthy first check must not alert: %+v", e.srv.monitor.Alerts())
	}

	events, unsubscribe := e.srv.hub.Subscribe()
	defer unsubscribe()
	full.Store(true)
	if _, err := e.srv.hw.startRun(ctx, []string{h.ID}); err != nil {
		t.Fatal(err)
	}
	e.srv.hw.jobs.Wait()
	alerts := e.srv.monitor.Alerts()
	if len(alerts) != 1 || alerts[0].Kind != "hardware" || alerts[0].To != health.LevelCrit ||
		alerts[0].Hardware[0].ID != "filesystem.space_low" || alerts[0].Hardware[0].Change != changeNew {
		t.Fatalf("problem alert: %+v", alerts)
	}
	var sawAlert bool
	for len(events) > 0 {
		var m struct {
			Type string       `json:"type"`
			Data health.Alert `json:"data"`
		}
		if json.Unmarshal(<-events, &m) == nil && m.Type == "alert" && m.Data.Kind == "hardware" {
			sawAlert = true
		}
	}
	if !sawAlert {
		t.Fatal("no alert event")
	}
	res, body = e.call("GET", "/api/alerts", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `"kind":"hardware"`) || !strings.Contains(body, `"vi":`) {
		t.Fatalf("alerts API: %d %.300s", res.StatusCode, body)
	}
	if f := e.srv.hw.fleet(); f.Run == nil || len(f.Run.OK) != 1 || f.Hosts[0].Summary.Headline != "crit" {
		t.Fatalf("fleet after the run: %+v %+v", f.Run, f.Hosts[0].Summary)
	}

	// Space freed: a recovery alert.
	full.Store(false)
	if _, err := e.srv.hw.startRun(ctx, []string{h.ID}); err != nil {
		t.Fatal(err)
	}
	e.srv.hw.jobs.Wait()
	alerts = e.srv.monitor.Alerts()
	if len(alerts) != 2 || alerts[0].To != health.LevelOK || alerts[0].Hardware[0].Change != changeResolved {
		t.Fatalf("recovery alert: %+v", alerts)
	}
}
