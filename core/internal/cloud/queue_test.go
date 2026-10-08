package cloud

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/health"
)

func ev(id string) Event {
	return Event{ID: id, Kind: "health", Level: "crit", Host: EventHost{Name: "srv"}, Title: Text{EN: "t", VI: "t"}}
}

func TestQueuePersistsAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	now := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	q := loadQueue(path, now)
	q.push(ev("a"), now)
	q.push(ev("b"), now)
	q.push(ev("a"), now) // same id: once
	if err := q.save(); err != nil {
		t.Fatal(err)
	}
	q2 := loadQueue(path, now)
	if len(q2.items) != 2 || q2.items[0].Event.ID != "a" || q2.items[1].Event.ID != "b" {
		t.Fatalf("reloaded %+v", q2.items)
	}
	if !q2.items[0].QueuedAt.Equal(now) {
		t.Errorf("queuedAt lost: %v", q2.items[0].QueuedAt)
	}
	q2.clear()
	if err := q2.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("an empty queue removes its file")
	}
	// A damaged file is an empty queue, not a crash.
	_ = os.WriteFile(path, []byte("{nope"), 0o600)
	if q3 := loadQueue(path, now); len(q3.items) != 0 {
		t.Error("damaged queue file must load empty")
	}
}

// The queue file is read back as-is; a tampered or foreign file must not
// outgrow the bounds, keep entries forever, or carry events the server would
// refuse (one of them used to fail the whole batch it was sent in).
func TestTamperedQueueFileIsSanitized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	now := time.Date(2026, 10, 3, 8, 0, 0, 0, time.UTC)
	var items []queued
	for i := range queueMax + 100 {
		items = append(items, queued{Event: ev(fmt.Sprint("e", i)), QueuedAt: now.Add(-time.Minute)})
	}
	bad := []Event{
		{ID: "", Kind: "health", Level: "crit", Host: EventHost{Name: "s"}, Title: Text{EN: "t"}},
		{ID: "k", Kind: "evil", Level: "crit", Host: EventHost{Name: "s"}, Title: Text{EN: "t"}},
		{ID: "l", Kind: "health", Level: "boom", Host: EventHost{Name: "s"}, Title: Text{EN: "t"}},
		{ID: "h", Kind: "health", Level: "crit", Title: Text{EN: "t"}},
		{ID: "t", Kind: "health", Level: "crit", Host: EventHost{Name: "s"}},
		{ID: "x\n", Kind: "health", Level: "crit", Host: EventHost{Name: "s"}, Title: Text{EN: "t"}},
		{ID: strings.Repeat("i", 129), Kind: "health", Level: "crit", Host: EventHost{Name: "s"}, Title: Text{EN: "t"}},
	}
	for _, e := range bad {
		items = append(items, queued{Event: e, QueuedAt: now})
	}
	items = append(items, queued{Event: ev("future"), QueuedAt: now.Add(1000 * time.Hour), NextAt: now.Add(1000 * time.Hour)})
	b, _ := json.Marshal(items)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	q := loadQueue(path, now)
	if len(q.items) != queueMax {
		t.Fatalf("loaded %d items, want at most %d", len(q.items), queueMax)
	}
	for _, it := range q.items {
		if !validEvent(it.Event) {
			t.Fatalf("invalid event kept: %+v", it.Event)
		}
	}
	last := q.items[len(q.items)-1]
	if last.Event.ID != "future" || last.QueuedAt.After(now) || last.NextAt.After(now.Add(retryMaxDelay)) {
		t.Fatalf("future times must be clamped: %+v", last)
	}
	if n := q.prune(now.Add(queueTTL)); n != queueMax {
		t.Fatalf("every entry must expire after %v, pruned %d", queueTTL, n)
	}
}

func TestQueueBoundsAndExpiry(t *testing.T) {
	now := time.Now()
	q := &queue{}
	for i := range queueMax + 7 {
		q.push(ev(fmt.Sprint(i)), now)
	}
	if len(q.items) != queueMax || q.items[0].Event.ID != "7" {
		t.Fatalf("bounded queue: %d items, first %s", len(q.items), q.items[0].Event.ID)
	}
	q = &queue{}
	q.push(ev("old"), now.Add(-25*time.Hour))
	q.push(ev("new"), now.Add(-time.Hour))
	if n := q.prune(now); n != 1 || len(q.items) != 1 || q.items[0].Event.ID != "new" {
		t.Fatalf("prune: %d, %+v", n, q.items)
	}
}

func TestQueueBatchesAndBackoff(t *testing.T) {
	now := time.Now()
	q := &queue{}
	for i := range 45 {
		q.push(ev(fmt.Sprint(i)), now)
	}
	b := q.ready(now, batchMax)
	if len(b) != 20 || b[0].ID != "0" || b[19].ID != "19" {
		t.Fatalf("first batch: %d", len(b))
	}
	ids := map[string]bool{}
	for _, e := range b {
		ids[e.ID] = true
	}
	q.remove(ids)
	if len(q.ready(now, batchMax)) != 20 || len(q.items) != 25 {
		t.Fatalf("after remove: %d left", len(q.items))
	}

	q = &queue{}
	q.push(ev("x"), now)
	for attempt := 1; attempt < maxAttempts; attempt++ {
		if d := q.retry(map[string]bool{"x": true}, now); d != 0 {
			t.Fatalf("dropped too early at attempt %d", attempt)
		}
		want := backoff(retryBase, attempt-1, retryMaxDelay)
		if got := q.items[0].NextAt.Sub(now); got != want {
			t.Errorf("attempt %d: next in %v, want %v", attempt, got, want)
		}
		if len(q.ready(now, 20)) != 0 {
			t.Fatal("an event waiting for its retry must not be ready")
		}
	}
	if d := q.retry(map[string]bool{"x": true}, now); d != 1 || len(q.items) != 0 {
		t.Fatal("an event must be dropped after maxAttempts")
	}
	if backoff(time.Second, 30, time.Minute) != time.Minute {
		t.Error("backoff must be capped")
	}
}

func TestForwardingFilters(t *testing.T) {
	all := DefaultForwarding()
	for _, l := range []string{"crit", "warn", "ok"} {
		if !all.allows(l) {
			t.Errorf("default must forward %s", l)
		}
	}
	if all.allows("info") || all.allows("") {
		t.Error("info/unknown are never forwarded")
	}
	f := Forwarding{Critical: true}
	if !f.allows("crit") || f.allows("warn") || f.allows("ok") {
		t.Error("only critical")
	}
	f = Forwarding{Warnings: true, Recoveries: true}
	if f.allows("crit") || !f.allows("warn") || !f.allows("ok") {
		t.Error("warnings and recoveries")
	}
	if normLang("vi-VN") != "vi" || normLang("en-US") != "en" || normLang("") != "en" {
		t.Error("normLang")
	}
}

func TestHealthAlertToEvent(t *testing.T) {
	at := time.Date(2026, 10, 3, 8, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	a := health.Alert{
		ID: "abc123", HostID: "h1", HostName: "srv-db01", From: health.LevelOK, To: health.LevelCrit, At: at,
		Findings: []health.Finding{
			{Code: "disk", Level: health.LevelCrit, Subject: "/var", Value: 96},
			{Code: "cpu", Level: health.LevelWarn, Value: 88},
			{Code: "reboot_required", Level: health.LevelInfo},
		},
	}
	e, ok := toEvent(a, "10.0.0.21")
	if !ok {
		t.Fatal("not converted")
	}
	if e.ID != "tw-abc123" || e.Kind != "health" || e.Level != "crit" {
		t.Fatalf("event %+v", e)
	}
	if e.Host != (EventHost{Name: "srv-db01", Address: "10.0.0.21"}) {
		t.Errorf("host %+v", e.Host)
	}
	if e.Title.EN != "srv-db01 is critical" || e.Title.VI != "srv-db01 đang nghiêm trọng" {
		t.Errorf("title %+v", e.Title)
	}
	if e.Body.EN != "• /var is 96% full\n• CPU 88%" || e.Body.VI != "• /var đã đầy 96%\n• CPU 88%" {
		t.Errorf("body %q / %q", e.Body.EN, e.Body.VI)
	}
	if !e.At.Equal(at) || e.At.Location() != time.UTC {
		t.Errorf("at %v", e.At)
	}
	// The same alert always gives the same id (retries are deduplicated).
	if e2, _ := toEvent(a, "10.0.0.21"); e2.ID != e.ID {
		t.Error("unstable id")
	}

	down := health.Alert{ID: "d", HostName: "web", To: health.LevelDown, Findings: []health.Finding{{Code: "unreachable", Level: health.LevelDown}}}
	e, _ = toEvent(down, "")
	if e.Level != "crit" || e.Title.EN != "web is down" || e.Body.EN != "Server is unreachable" || e.Body.VI != "Không kết nối được máy chủ" {
		t.Errorf("down: %+v", e)
	}
	ok2 := health.Alert{ID: "o", HostName: "web", From: health.LevelCrit, To: health.LevelOK, Findings: []health.Finding{}}
	e, _ = toEvent(ok2, "")
	if e.Level != "ok" || e.Title.VI != "web đã ổn định trở lại" || e.Body.EN != "" {
		t.Errorf("recovery: %+v", e)
	}
	if _, ok := toEvent(health.Alert{ID: "u", To: health.LevelUnknown}, ""); ok {
		t.Error("unknown level must not be forwarded")
	}
	if _, ok := toEvent(health.Alert{To: health.LevelCrit}, ""); ok {
		t.Error("an alert without id cannot be deduplicated and must not be forwarded")
	}
}

func TestHardwareAlertToEvent(t *testing.T) {
	title := health.Text{EN: "srv has a critical hardware problem", VI: "srv có lỗi phần cứng nghiêm trọng"}
	body := health.Text{EN: "Disk failing (/dev/sda) · 1 more", VI: "Ổ đĩa sắp hỏng (/dev/sda) · và 1 mục khác"}
	a := health.Alert{
		ID: "hw1", HostName: "srv", Kind: "hardware", From: health.LevelOK, To: health.LevelCrit,
		Title: &title, Body: &body,
		Hardware: []health.HardwareChange{
			{Change: "new", ID: "disk.smart_failed", Target: "/dev/sda", To: "crit", Title: health.Text{EN: "Disk failing", VI: "Ổ đĩa sắp hỏng"}},
			{Change: "new", ID: "mem.ecc", To: "warn", Title: health.Text{EN: "ECC errors", VI: "Lỗi ECC"}},
		},
	}
	e, ok := toEvent(a, "10.0.0.5")
	if !ok || e.Kind != "hardware" || e.Level != "crit" || e.ID != "tw-hw1" {
		t.Fatalf("event %+v", e)
	}
	if e.Title.EN != title.EN || e.Title.VI != title.VI {
		t.Errorf("title %+v", e.Title)
	}
	if e.Body.EN != "• Disk failing (/dev/sda)\n• ECC errors" || e.Body.VI != "• Ổ đĩa sắp hỏng (/dev/sda)\n• Lỗi ECC" {
		t.Errorf("body %q / %q", e.Body.EN, e.Body.VI)
	}
	// A single change keeps the alert's own body.
	a.Hardware = a.Hardware[:1]
	e, _ = toEvent(a, "")
	if e.Body.EN != body.EN {
		t.Errorf("single change body %q", e.Body.EN)
	}
}

func TestEventTextsAreCut(t *testing.T) {
	long := strings.Repeat("é", 3000)
	title := health.Text{EN: long, VI: long}
	a := health.Alert{ID: "x", HostName: "h", Kind: "hardware", To: health.LevelWarn, Title: &title, Body: &title}
	e, _ := toEvent(a, "")
	if n := len([]rune(e.Title.EN)); n != titleMax {
		t.Errorf("title %d runes", n)
	}
	if n := len([]rune(e.Body.VI)); n != bodyMax {
		t.Errorf("body %d runes", n)
	}
	lines := make([]string, 14)
	for i := range lines {
		lines[i] = fmt.Sprint("line ", i)
	}
	if got := joinLines(lines, "vi"); !strings.HasSuffix(got, "\n… và 4 mục khác") || strings.Count(got, "•") != maxLines {
		t.Errorf("joinLines: %q", got)
	}
}

// state.json and queue.json must end up owner-only even when a leftover
// temp file with looser permissions (or a symlink) is in the way.
func TestAtomicWriteIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path+".tmp", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path+".tmp", 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode %v, want 0600", fi.Mode().Perm())
	}

	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path + ".tmp")
	if err := os.Symlink(outside, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Fatalf("write followed a symlink: %q", b)
	}
	if b, _ := os.ReadFile(path); string(b) != `{"a":2}` {
		t.Fatalf("state %q", b)
	}
}
