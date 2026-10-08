package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A data dir that was signed in to one server and is then started against
// another (TERMWARD_CLOUD_URL, e.g. a local `wrangler dev`) must not send that
// server the first one's signed requests (the host is not signed, so they
// could be replayed to the first server for 300 s), and the other server's
// "device_revoked" must not delete the first server's key.
func TestOtherServerKeepsItsOwnState(t *testing.T) {
	ctx := context.Background()
	prod := newFakeCloud(t)
	e := newEnv(t, prod, "m")
	e.signIn(t, "a@b.vn")

	other := newFakeCloud(t)
	o := newEnvAt(t, other, e.dir, e.sec, "m")
	if o.s.Status().SignedIn {
		t.Error("another server must not inherit this account")
	}
	_, _ = o.s.Refresh(ctx)
	other.mu.Lock()
	sent := append([]string(nil), other.log...)
	other.mu.Unlock()
	if len(sent) > 0 {
		t.Errorf("requests of the first server's device went to another server: %v", sent)
	}
	if o.s.keyName() == e.s.keyName() {
		t.Error("two servers must not share a device key")
	}

	// The production server keeps the directory existing installs already use.
	if got := stateDir(e.dir, DefaultBaseURL); got != filepath.Join(e.dir, "cloud") {
		t.Errorf("production state moved to %s", got)
	}

	back := newEnvAt(t, prod, e.dir, e.sec, "m")
	if _, err := back.s.Refresh(ctx); err != nil || !back.s.Status().SignedIn {
		t.Fatalf("the first server's sign-in must survive a run against another server: %v %+v", err, back.s.Status())
	}
}

// An alert that was being converted while the user signed out belongs to the
// account that was signed in; it must not wait in the queue and go out under
// the next account (another customer's channels).
func TestAlertDuringSignOutIsNotSentForTheNextAccount(t *testing.T) {
	fast(t)
	e := readyToForward(t)
	entered, release := make(chan struct{}), make(chan struct{})
	e.s.host = func(string) (string, bool) {
		close(entered)
		<-release
		return "10.0.0.1", true
	}
	done := make(chan struct{})
	go func() { e.s.accept(crit("old-account", "1")); close(done) }()
	<-entered
	if _, err := e.s.SignOut(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if n := e.s.Status().Queue.Pending; n != 0 {
		t.Errorf("%d alerts of the signed-out account still queued", n)
	}

	e.s.host = nil
	e.signIn(t, "c@d.vn")
	e.run(t)
	time.Sleep(200 * time.Millisecond)
	for _, ev := range e.f.events() {
		if ev.ID == "tw-old-account" {
			t.Fatal("an alert of the previous account was sent under the new one")
		}
	}
}

// TERMWARD_CLOUD_URL may carry credentials (https://user:token@host); the
// refusal is logged, so it must not repeat them.
func TestRefusedCloudURLIsNotEchoed(t *testing.T) {
	for _, bad := range []string{"https://user:s3cr3t@example.dev", "https://example.dev/?key=s3cr3t", "http://example.com/s3cr3t"} {
		_, err := ResolveBaseURL(bad)
		if err == nil {
			t.Fatalf("%q accepted", bad)
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("error repeats the URL: %v", err)
		}
	}
}

// A queue file edited by hand may carry texts far beyond the contract's
// limits. One such event would push its whole batch over the server's 512 KiB
// request limit, and the 413 drops the batch: the genuine alerts in it too.
func TestTamperedQueueTextsAreBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	now := time.Now()
	huge := ev("huge")
	big := strings.Repeat("x", 600<<10)
	huge.Title, huge.Body = Text{EN: big, VI: big}, Text{EN: big, VI: big}
	huge.Host = EventHost{Name: big, Address: big}
	items := []queued{{Event: huge, QueuedAt: now}}
	for i := range batchMax - 1 {
		items = append(items, queued{Event: ev(fmt.Sprint("genuine", i)), QueuedAt: now})
	}
	b, _ := json.Marshal(items)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	q := loadQueue(path, now)
	body, _ := jsonBody(map[string]any{"lang": "vi", "events": q.ready(now, batchMax)})
	if len(body) > 512<<10 {
		t.Fatalf("one tampered event makes a %d KiB request (limit 512 KiB)", len(body)>>10)
	}
}
