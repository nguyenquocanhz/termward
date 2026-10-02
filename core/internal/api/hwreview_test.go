package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nguyenquocanhz/diagward/model"

	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// A previous check that never looked at a component (cut off early, or a
// tool such as smartctl was missing) is no baseline for it: its long-standing
// warnings must not page anyone as "new" once it is checked.
func TestDiffHardwareUncheckedComponentInPrevIsNoBaseline(t *testing.T) {
	prev := result("root", true) // partial: the disk section never arrived
	for i := range prev.Report.Summary {
		if prev.Report.Summary[i].Component == "disk" {
			prev.Report.Summary[i].Checked = false
		}
	}
	cur := result("root", false,
		finding("disk.smart", "/dev/sda", "disk", model.Warn),
		finding("disk.smart", "/dev/sdb", "disk", model.Crit),
		finding("memory.ecc", "DIMM A1", "memory", model.Warn))
	if got := changes(diffHardware(prev, cur)); got != "new:disk.smart@/dev/sdb new:memory.ecc@DIMM A1" {
		t.Fatalf("got %q", got)
	}
}

// Quitting Termward cancels running unattended checks. That is not a failure
// of the server: nothing must be recorded as "could not connect".
func TestSchedulerShutdownIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	e := newSchedEnv(t, dir, 1, store.HardwareDaily)
	id := e.hosts[0].ID
	started := make(chan struct{})
	e.hs.check = func(ctx context.Context, _ string, _ hwSource) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.hs.tick(ctx)
	e.clock.add(3 * time.Hour)
	e.hs.tick(ctx)
	<-started
	cancel()
	e.hs.jobs.Wait()
	if p := e.plan(id); p.Error != "" {
		t.Fatalf("shutdown recorded as a failure: %+v", p)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, hwScheduleFile))
	if strings.Contains(string(raw), "connect_failed") {
		t.Fatalf("shutdown persisted as a failure: %s", raw)
	}
}

// Deleting a host before the scheduler first ticks must not wipe the saved
// schedule of every other host.
func TestForgetBeforeLoadKeepsSchedule(t *testing.T) {
	dir := t.TempDir()
	e := newSchedEnv(t, dir, 2, store.HardwareDaily)
	e.hs.tick(context.Background())
	keep := e.plan(e.hosts[0].ID).NextRun

	e2 := newSchedEnv(t, dir, 2, store.HardwareDaily)
	e2.hs.forget(e.hosts[1].ID)
	if p := e2.plan(e.hosts[0].ID); !p.NextRun.Equal(keep) {
		t.Fatalf("schedule lost: %v, want %v", p.NextRun, keep)
	}
}

// A scheduled check that waited for a free slot while someone checked the
// host by hand is not run again right after.
func TestQueuedScheduledCheckSkipsAfterManualCheck(t *testing.T) {
	e := newSchedEnv(t, t.TempDir(), 1, store.HardwareDaily)
	id := e.hosts[0].ID
	var calls atomic.Int32
	e.hs.check = func(context.Context, string, hwSource) error { calls.Add(1); return nil }
	for range hwMaxConcurrent { // every slot busy
		e.hs.sem <- struct{}{}
	}
	ctx := context.Background()
	e.hs.tick(ctx)
	e.clock.add(3 * time.Hour)
	e.hs.tick(ctx) // queued, waiting for a slot
	e.succeed(id, srcManual)
	for range hwMaxConcurrent {
		<-e.hs.sem
	}
	e.hs.jobs.Wait()
	if calls.Load() != 0 {
		t.Fatalf("checked again right after a manual check (%d)", calls.Load())
	}
	if v := e.hs.fleet(); v.Hosts[0].State != "" {
		t.Fatalf("state: %+v", v.Hosts[0])
	}
}

// A check cut off because Termward is quitting must not replace the last
// complete result (nor become the baseline for alerts).
func TestShutdownPartialResultIsNotSaved(t *testing.T) {
	var cut atomic.Bool
	sctx, scancel := context.WithCancel(context.Background())
	defer scancel()
	base := linuxHandler(t, true)
	e := newHWEnv(t, func(cmd string, stdin []byte) (string, int) {
		if cmd == "sh -s" && cut.Load() {
			scancel()
			return fakeCollectorOutput(string(stdin), "root", false), 0
		}
		return base(cmd, stdin)
	})
	if _, err := e.srv.runCheck(context.Background(), e.host, hardwareInput{}, srcSchedule); err != nil {
		t.Fatal(err)
	}
	e.srv.ctx = sctx
	cut.Store(true)
	_, err := e.srv.runCheck(context.Background(), e.host, hardwareInput{}, srcSchedule)
	res, lerr := loadHardware(e.dir, e.host.ID)
	if lerr != nil || res.Partial {
		t.Fatalf("a shutdown replaced the complete result (err %v): partial=%v", err, res != nil && res.Partial)
	}
}

// Turning scheduled checks off stops the ones still waiting for a slot.
func TestQueuedScheduledCheckSkipsWhenTurnedOff(t *testing.T) {
	e := newSchedEnv(t, t.TempDir(), 1, store.HardwareDaily)
	var calls atomic.Int32
	e.hs.check = func(context.Context, string, hwSource) error { calls.Add(1); return nil }
	for range hwMaxConcurrent {
		e.hs.sem <- struct{}{}
	}
	ctx := context.Background()
	e.hs.tick(ctx)
	e.clock.add(3 * time.Hour)
	e.hs.tick(ctx)
	set := e.st.Settings()
	set.HardwareInterval = store.HardwareOff
	e.st.SaveSettings(set)
	for range hwMaxConcurrent {
		<-e.hs.sem
	}
	e.hs.jobs.Wait()
	if calls.Load() != 0 {
		t.Fatal("a scheduled check ran after scheduling was turned off")
	}
}
