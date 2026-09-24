package health

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

const fixture = `Welcome to a noisy .bashrc
os=Ubuntu 24.04.5 LTS
kernel=6.8.0-85-generic
hostname=web-01
nproc=4
uptime=356712.45
load=0.52 0.61 0.58
cpu0=cpu  1000 0 500 8000 500 0 0 0 0 0
cpu1=cpu  1060 0 530 8090 510 0 0 0 0 0
mem.MemTotal=8000000
mem.MemFree=1000000
mem.MemAvailable=2000000
mem.Buffers=100000
mem.Cached=900000
mem.SwapTotal=2000000
mem.SwapFree=1500000
disk=/|100000|85000|10000
disk=/boot/efi|500000|6000|494000
disk=/|100000|85000|10000
systemd=1
failed=nginx.service
docker=ok
ctr=db|running|Up 3 hours (healthy)
ctr=api|running|Up 2 minutes (unhealthy)
ctr=worker|restarting|Restarting (1) 5 seconds ago
ctr=job|exited|Exited (0) 2 days ago
reboot=1
`

func TestParse(t *testing.T) {
	s := Parse(fixture)
	if s.OS != "Ubuntu 24.04.5 LTS" || s.Hostname != "web-01" || s.CPUs != 4 {
		t.Fatalf("identity fields: %+v", s)
	}
	if s.Load != [3]float64{0.52, 0.61, 0.58} {
		t.Errorf("load = %v", s.Load)
	}
	// Δtotal = 190, Δidle (idle+iowait) = 100 → 90/190 busy.
	if s.CPUPercent != 47.4 {
		t.Errorf("cpu = %v, want 47.4", s.CPUPercent)
	}
	if s.MemTotalKB != 8000000 || s.MemUsedKB != 6000000 || s.MemPercent != 75 {
		t.Errorf("mem = %d/%d %.1f%%", s.MemUsedKB, s.MemTotalKB, s.MemPercent)
	}
	if s.SwapUsedKB != 500000 {
		t.Errorf("swap used = %d", s.SwapUsedKB)
	}
	if len(s.Disks) != 2 || s.Disks[0].Mount != "/" || s.Disks[0].Percent != 89.5 {
		t.Errorf("disks = %+v", s.Disks)
	}
	if len(s.FailedUnits) != 1 || s.Docker != "ok" || len(s.Containers) != 4 || !s.Reboot {
		t.Errorf("services: %+v", s)
	}
	if s.Containers[1].Health != "unhealthy" || s.Containers[0].Health != "healthy" {
		t.Errorf("container health: %+v", s.Containers)
	}
}

func TestCPUPercent(t *testing.T) {
	a := []uint64{100, 0, 100, 700, 100, 0, 0, 0}
	b := []uint64{150, 0, 150, 750, 150, 0, 0, 0} // +200 total, +100 idle(incl. iowait)
	if got := cpuPercent(a, b); got != 50 {
		t.Errorf("cpuPercent = %v, want 50", got)
	}
	if got := cpuPercent(a, a); got != -1 {
		t.Errorf("no delta should be unknown, got %v", got)
	}
}

func TestParseOldKernelWithoutMemAvailable(t *testing.T) {
	s := Parse("mem.MemTotal=1000\nmem.MemFree=200\nmem.Buffers=100\nmem.Cached=200\n")
	if s.MemPercent != 50 {
		t.Errorf("mem%% = %v, want 50", s.MemPercent)
	}
}

func TestEvaluate(t *testing.T) {
	th := store.DefaultSettings().Thresholds
	level, fs := Evaluate(Parse(fixture), th)
	if level != LevelCrit {
		t.Fatalf("level = %s, want crit", level)
	}
	codes := map[string]bool{}
	for _, f := range fs {
		codes[f.Code+":"+f.Subject] = true
	}
	for _, want := range []string{"disk:/", "unit_failed:nginx.service", "container_unhealthy:api", "container_restarting:worker", "reboot_required:"} {
		if !codes[want] {
			t.Errorf("missing finding %s in %+v", want, fs)
		}
	}
	if codes["container_dead:job"] || codes["disk:/boot/efi"] {
		t.Errorf("unexpected findings: %+v", fs)
	}
	if fs[0].Level != LevelCrit || fs[len(fs)-1].Level != LevelInfo {
		t.Errorf("findings not sorted by severity: %+v", fs)
	}

	healthy := Sample{CPUPercent: 10, MemPercent: 20, CPUs: 2, Load: [3]float64{0.1, 0.1, 0.1}}
	if level, fs := Evaluate(healthy, th); level != LevelOK || len(fs) != 0 {
		t.Errorf("healthy host graded %s %+v", level, fs)
	}
}

// ------------------------------------------------------------ monitor

type fakeRunner struct {
	mu  sync.Mutex
	out string
	err error
}

func (f *fakeRunner) set(out string, err error) {
	f.mu.Lock()
	f.out, f.err = out, err
	f.mu.Unlock()
}

func (f *fakeRunner) Run(_ context.Context, _, _ string, _ io.Reader) (sshx.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return sshx.Result{Stdout: f.out}, f.err
}

type recorder struct {
	mu     sync.Mutex
	alerts []Alert
}

func (r *recorder) publish(kind string, v any) {
	if kind == "alert" {
		r.mu.Lock()
		r.alerts = append(r.alerts, v.(Alert))
		r.mu.Unlock()
	}
}

func newMonitor(t *testing.T) (*Monitor, *fakeRunner, *recorder, string) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h, err := st.SaveHost(store.Host{Name: "web", Address: "10.0.0.1", User: "root", Auth: store.AuthAgent, Monitor: true})
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{}
	rec := &recorder{}
	return NewMonitor(st, fr, rec.publish), fr, rec, h.ID
}

const okOut = "nproc=2\nload=0.1 0.1 0.1\nmem.MemTotal=100\nmem.MemAvailable=80\ndisk=/|100|10|90\n"
const diskFullOut = "nproc=2\nload=0.1 0.1 0.1\nmem.MemTotal=100\nmem.MemAvailable=80\ndisk=/|100|97|3\n"

func TestMonitorConfirmsBeforeAlerting(t *testing.T) {
	m, fr, rec, id := newMonitor(t)
	ctx := context.Background()

	fr.set(okOut, nil)
	m.Poll(ctx, id)
	if s := m.Snapshot()[0]; s.Level != LevelOK || len(rec.alerts) != 0 {
		t.Fatalf("first ok poll: level %s, %d alerts", s.Level, len(rec.alerts))
	}

	fr.set(diskFullOut, nil)
	m.Poll(ctx, id)
	if s := m.Snapshot()[0]; s.Level != LevelOK || s.Pending != LevelCrit || len(rec.alerts) != 0 {
		t.Fatalf("single spike must not alert: level %s pending %s alerts %d", s.Level, s.Pending, len(rec.alerts))
	}
	m.Poll(ctx, id)
	if s := m.Snapshot()[0]; s.Level != LevelCrit || len(rec.alerts) != 1 || rec.alerts[0].To != LevelCrit {
		t.Fatalf("confirmed crit: level %s alerts %+v", s.Level, rec.alerts)
	}

	fr.set(okOut, nil)
	m.Poll(ctx, id)
	if s := m.Snapshot()[0]; s.Level != LevelOK || len(rec.alerts) != 2 || rec.alerts[1].To != LevelOK {
		t.Fatalf("recovery: level %s alerts %+v", s.Level, rec.alerts)
	}
	if n := len(m.Snapshot()[0].History); n != 4 {
		t.Errorf("history points = %d, want 4", n)
	}
}

func TestMuteHoldsBackAlertsDuringPlannedReboot(t *testing.T) {
	m, fr, rec, id := newMonitor(t)
	ctx := context.Background()
	fr.set(okOut, nil)
	m.Poll(ctx, id)

	m.Mute(id, time.Now().Add(time.Hour))
	fr.set("", errors.New("connection refused"))
	m.Poll(ctx, id)
	m.Poll(ctx, id)
	if s := m.Snapshot()[0]; s.Level != LevelDown || s.MutedUntil == nil {
		t.Fatalf("status should still show down while muted: %+v", s)
	}
	fr.set(okOut, nil)
	m.Poll(ctx, id)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.alerts) != 0 {
		t.Fatalf("muted host must not alert, got %+v", rec.alerts)
	}
}

func TestAlertText(t *testing.T) {
	a := Alert{HostName: "web", To: LevelCrit, Findings: []Finding{
		{Code: "reboot_required", Level: LevelInfo},
		{Code: "disk", Level: LevelCrit, Subject: "/var", Value: 96.4},
	}}
	if title, body := AlertText(a, "en"); title != "web is critical" || body != "/var is 96% full" {
		t.Errorf("en: %q / %q", title, body)
	}
	if title, body := AlertText(a, "vi"); title != "web đang nghiêm trọng" || body != "/var đã đầy 96%" {
		t.Errorf("vi: %q / %q", title, body)
	}
}

func TestMonitorDownAndConfigErrors(t *testing.T) {
	m, fr, rec, id := newMonitor(t)
	ctx := context.Background()

	fr.set("", errors.New("dial tcp 10.0.0.1:22: i/o timeout"))
	m.Poll(ctx, id)
	if s := m.Snapshot()[0]; s.Level != LevelDown || len(rec.alerts) != 1 {
		t.Fatalf("host down on first poll should alert once: %s %+v", s.Level, rec.alerts)
	}

	fr.set("", &sshx.AuthRequiredError{HostID: id, Kind: "password"})
	m.Poll(ctx, id)
	s := m.Snapshot()[0]
	if s.Level != LevelUnknown || s.ErrorKind != "auth_required" || len(rec.alerts) != 1 {
		t.Fatalf("auth problems must not alert: %s %s %+v", s.Level, s.ErrorKind, rec.alerts)
	}
}
