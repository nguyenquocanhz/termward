package cloud

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Alert queue limits. Events wait on disk until the server has answered for
// them, so alerts raised while offline (or while Termward restarts) still go
// out, but nothing is kept forever.
const (
	queueMax      = 500            // oldest events are dropped beyond this
	queueTTL      = 24 * time.Hour // the server dedupes ids for 24 h too
	batchMax      = 20             // the server's limit per request
	maxAttempts   = 8              // per event, when every channel failed
	retryMaxDelay = 15 * time.Minute
)

// retryBase is the first retry delay of an event no channel accepted
// (a variable so tests run faster).
var retryBase = 30 * time.Second

type queued struct {
	Event    Event     `json:"event"`
	QueuedAt time.Time `json:"queuedAt"`
	Attempts int       `json:"attempts,omitempty"`
	NextAt   time.Time `json:"nextAt,omitempty"`
}

// queue is not safe for concurrent use; Service guards it.
type queue struct {
	path  string
	items []queued
}

// loadQueue reads the queue file. A missing or damaged file is an empty
// queue: losing pending alerts is better than not starting. The file is not
// trusted further than the queue's own bounds: events the server would refuse
// are dropped, texts are cut to the contract's lengths, at most queueMax (the
// newest) are kept, and times in the future are brought back to now so every
// entry still expires.
func loadQueue(path string, now time.Time) *queue {
	q := &queue{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return q
	}
	var items []queued
	if json.Unmarshal(b, &items) != nil {
		return q
	}
	for _, it := range items {
		if !validEvent(it.Event) || q.has(it.Event.ID) {
			continue
		}
		if it.QueuedAt.After(now) {
			it.QueuedAt = now
		}
		if it.NextAt.After(now.Add(retryMaxDelay)) {
			it.NextAt = now
		}
		it.Attempts = min(max(it.Attempts, 0), maxAttempts-1)
		// The cuts toEvent makes, so one edited entry cannot push its batch
		// over the server's request size limit (a 413 drops the whole batch).
		e := &it.Event
		e.Host = EventHost{Name: cut(e.Host.Name, 253), Address: cut(e.Host.Address, 253)}
		e.Title = Text{EN: cut(e.Title.EN, titleMax), VI: cut(e.Title.VI, titleMax)}
		e.Body = Text{EN: cut(e.Body.EN, bodyMax), VI: cut(e.Body.VI, bodyMax)}
		q.items = append(q.items, it)
	}
	if over := len(q.items) - queueMax; over > 0 {
		q.items = q.items[over:]
	}
	return q
}

// validEvent mirrors the server's per-event checks (src/routes/alerts.ts).
func validEvent(e Event) bool {
	if e.ID == "" || len(e.ID) > 128 || strings.ContainsFunc(e.ID, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	if e.Kind != "health" && e.Kind != "hardware" {
		return false
	}
	switch e.Level {
	case "crit", "warn", "info", "ok":
	default:
		return false
	}
	return strings.TrimSpace(e.Host.Name) != "" && (strings.TrimSpace(e.Title.EN) != "" || strings.TrimSpace(e.Title.VI) != "")
}

func (q *queue) save() error {
	if q.path == "" {
		return nil
	}
	if len(q.items) == 0 {
		err := os.Remove(q.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	b, err := json.Marshal(q.items)
	if err != nil {
		return err
	}
	return writeFileAtomic(q.path, b)
}

func (q *queue) has(id string) bool {
	for _, it := range q.items {
		if it.Event.ID == id {
			return true
		}
	}
	return false
}

// push adds an event (once per id) and returns how many old events were
// dropped to stay within queueMax.
func (q *queue) push(e Event, now time.Time) int {
	if q.has(e.ID) {
		return 0
	}
	q.items = append(q.items, queued{Event: e, QueuedAt: now})
	if over := len(q.items) - queueMax; over > 0 {
		q.items = append([]queued(nil), q.items[over:]...)
		return over
	}
	return 0
}

// prune drops events queued more than queueTTL ago.
func (q *queue) prune(now time.Time) int {
	keep := q.items[:0]
	for _, it := range q.items {
		if now.Sub(it.QueuedAt) < queueTTL {
			keep = append(keep, it)
		}
	}
	n := len(q.items) - len(keep)
	q.items = keep
	return n
}

// ready returns up to n events due now, oldest first.
func (q *queue) ready(now time.Time, n int) []Event {
	var out []Event
	for _, it := range q.items {
		if len(out) == n {
			break
		}
		if !it.NextAt.After(now) {
			out = append(out, it.Event)
		}
	}
	return out
}

// next is the earliest time an event becomes due (zero when empty).
func (q *queue) next() time.Time {
	var t time.Time
	for i, it := range q.items {
		if i == 0 || it.NextAt.Before(t) {
			t = it.NextAt
		}
	}
	return t
}

func (q *queue) remove(ids map[string]bool) {
	keep := q.items[:0]
	for _, it := range q.items {
		if !ids[it.Event.ID] {
			keep = append(keep, it)
		}
	}
	q.items = keep
}

// retry schedules events again with exponential backoff and drops those that
// have used up their attempts; it returns how many were dropped.
func (q *queue) retry(ids map[string]bool, now time.Time) int {
	keep := q.items[:0]
	dropped := 0
	for _, it := range q.items {
		if ids[it.Event.ID] {
			it.Attempts++
			if it.Attempts >= maxAttempts {
				dropped++
				continue
			}
			it.NextAt = now.Add(backoff(retryBase, it.Attempts-1, retryMaxDelay))
		}
		keep = append(keep, it)
	}
	q.items = keep
	return dropped
}

func (q *queue) clear() { q.items = nil }

// backoff is base·2^n, capped.
func backoff(base time.Duration, n int, maxDelay time.Duration) time.Duration {
	d := base
	for i := 0; i < n && d < maxDelay; i++ {
		d *= 2
	}
	return min(d, maxDelay)
}

// writeFileAtomic writes via a temp file and rename, readable by the owner only.
func writeFileAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// A fresh file (O_EXCL, mode 0600) rather than a fixed "<path>.tmp": an
	// existing temp file would keep its looser mode, or be a symlink followed.
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
