package health

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

const (
	historySize  = 120 // one hour at the default 30 s interval
	confirmPolls = 2   // a worse level must repeat before it is reported
	pollTimeout  = 25 * time.Second
	concurrency  = 16
	maxAlerts    = 200
)

type Point struct {
	T    int64   `json:"t"` // unix milliseconds
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
	Load float64 `json:"load"`
}

type Status struct {
	HostID    string    `json:"hostId"`
	Level     Level     `json:"level"`
	Pending   Level     `json:"pending,omitempty"` // worse level awaiting confirmation
	Since     time.Time `json:"since"`
	CheckedAt time.Time `json:"checkedAt"`
	Checking  bool      `json:"checking"`
	Findings  []Finding `json:"findings"`
	Sample    *Sample   `json:"sample,omitempty"`
	Error     string    `json:"error,omitempty"`
	ErrorKind string    `json:"errorKind,omitempty"`
	History   []Point   `json:"history"`
	// MutedUntil is set after a planned reboot/shutdown: alerts are held back.
	MutedUntil *time.Time `json:"mutedUntil,omitempty"`
}

type Alert struct {
	ID       string    `json:"id"`
	HostID   string    `json:"hostId"`
	HostName string    `json:"hostName"`
	From     Level     `json:"from"`
	To       Level     `json:"to"`
	Findings []Finding `json:"findings"`
	Error    string    `json:"error,omitempty"`
	At       time.Time `json:"at"`
}

// Runner is the part of sshx.Pool the monitor needs; tests substitute it.
type Runner interface {
	Run(ctx context.Context, hostID, cmd string, stdin io.Reader) (sshx.Result, error)
}

type Publisher func(kind string, payload any)

type Monitor struct {
	store   *store.Store
	runner  Runner
	publish Publisher

	mu     sync.Mutex
	states map[string]*hostState
	alerts []Alert
	wake   chan struct{}
}

type hostState struct {
	status   Status
	pendingN int
	inflight bool
}

func NewMonitor(st *store.Store, r Runner, pub Publisher) *Monitor {
	return &Monitor{
		store: st, runner: r, publish: pub,
		states: map[string]*hostState{},
		wake:   make(chan struct{}, 1),
	}
}

// Run polls every monitored host, then sleeps for the configured interval.
func (m *Monitor) Run(ctx context.Context) {
	for {
		m.pollAll(ctx)
		interval := time.Duration(m.store.Settings().PollIntervalSec) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		case <-m.wake:
		}
	}
}

// Mute holds back alerts for a host until the given time (planned maintenance).
// Status changes are still tracked and shown.
func (m *Monitor) Mute(hostID string, until time.Time) {
	m.mu.Lock()
	st := m.stateLocked(hostID)
	u := until.UTC()
	st.status.MutedUntil = &u
	snap := cloneStatus(st.status)
	m.mu.Unlock()
	m.publish("status", snap)
	go func() {
		time.Sleep(3 * time.Second)
		m.Poll(context.Background(), hostID)
	}()
}

// Wake starts a new round now (e.g. after settings or hosts changed).
func (m *Monitor) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Monitor) pollAll(ctx context.Context) {
	hosts := m.store.Hosts()
	m.prune(hosts)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, h := range hosts {
		if !h.Monitor {
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			m.Poll(ctx, id)
		}(h.ID)
	}
	wg.Wait()
}

func (m *Monitor) prune(hosts []store.Host) {
	keep := map[string]bool{}
	for _, h := range hosts {
		if h.Monitor {
			keep[h.ID] = true
		}
	}
	m.mu.Lock()
	var removed []string
	for id := range m.states {
		if !keep[id] {
			delete(m.states, id)
			removed = append(removed, id)
		}
	}
	m.mu.Unlock()
	for _, id := range removed {
		m.publish("status_removed", map[string]string{"hostId": id})
	}
}

func (m *Monitor) stateLocked(hostID string) *hostState {
	st, ok := m.states[hostID]
	if !ok {
		st = &hostState{status: Status{HostID: hostID, Level: LevelUnknown, Findings: []Finding{}, History: []Point{}}}
		m.states[hostID] = st
	}
	return st
}

// Poll checks one host now. Overlapping polls of the same host are skipped.
func (m *Monitor) Poll(ctx context.Context, hostID string) {
	m.mu.Lock()
	st := m.stateLocked(hostID)
	if st.inflight {
		m.mu.Unlock()
		return
	}
	st.inflight = true
	st.status.Checking = true
	snap := cloneStatus(st.status)
	m.mu.Unlock()
	m.publish("status", snap)

	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	start := time.Now()
	res, err := m.runner.Run(ctx, hostID, "sh -s", strings.NewReader(Script))
	m.apply(hostID, res, err, time.Since(start))
}

func (m *Monitor) apply(hostID string, res sshx.Result, err error, took time.Duration) {
	h, herr := m.store.Host(hostID)
	now := time.Now().UTC()

	var (
		raw    Level
		fs     []Finding
		sample *Sample
		kind   string
	)
	if err != nil {
		kind = errorKind(err)
		if kind == "network" {
			raw = LevelDown
			fs = []Finding{{Code: "unreachable", Level: LevelDown}}
		} else {
			raw = LevelUnknown
			fs = []Finding{}
		}
	} else {
		s := Parse(res.Stdout)
		s.At = now
		// The collector sleeps 1 s to sample CPU; don't count that as latency.
		s.LatencyMs = max(took.Milliseconds()-1000, 0)
		raw, fs = Evaluate(s, m.store.Settings().Thresholds)
		sample = &s
	}

	m.mu.Lock()
	st, ok := m.states[hostID]
	if !ok || herr != nil { // host deleted or unmonitored while polling
		m.mu.Unlock()
		return
	}
	st.inflight = false
	prev := st.status.Level
	st.status.Checking = false
	st.status.CheckedAt = now
	st.status.Findings = fs
	st.status.Error, st.status.ErrorKind = "", kind
	if err != nil {
		st.status.Error = err.Error()
	}
	if sample != nil {
		st.status.Sample = sample
		st.status.History = append(st.status.History, Point{
			T: now.UnixMilli(), CPU: sample.CPUPercent, Mem: sample.MemPercent, Load: sample.Load[0],
		})
		if n := len(st.status.History); n > historySize {
			st.status.History = slices.Clone(st.status.History[n-historySize:])
		}
	}

	var alert *Alert
	set := func(l Level) {
		st.status.Level, st.status.Since = l, now
		st.status.Pending, st.pendingN = "", 0
	}
	switch {
	case prev == LevelUnknown:
		// First real result (or recovered from a config problem): report it
		// only if it needs attention.
		set(raw)
		if raw == LevelCrit || raw == LevelDown {
			alert = &Alert{From: prev, To: raw}
		}
	case raw == LevelUnknown:
		set(raw) // credentials/host key problem: shown in the UI, not alerted
	case raw.rank() <= prev.rank():
		if raw != prev {
			set(raw)
			if raw == LevelOK {
				alert = &Alert{From: prev, To: raw} // recovered
			}
		} else {
			st.status.Pending, st.pendingN = "", 0
		}
	default: // worse: wait for confirmation to avoid alerting on a spike
		if st.status.Pending == raw {
			st.pendingN++
		} else {
			st.status.Pending, st.pendingN = raw, 1
		}
		if st.pendingN >= confirmPolls {
			set(raw)
			alert = &Alert{From: prev, To: raw}
		}
	}

	if mu := st.status.MutedUntil; mu != nil {
		if now.After(*mu) {
			st.status.MutedUntil = nil
		} else {
			alert = nil
		}
	}
	if alert != nil {
		alert.ID = store.NewID()
		alert.HostID, alert.HostName = hostID, h.Name
		alert.Findings, alert.Error, alert.At = fs, st.status.Error, now
		m.alerts = append(m.alerts, *alert)
		if len(m.alerts) > maxAlerts {
			m.alerts = slices.Clone(m.alerts[len(m.alerts)-maxAlerts:])
		}
	}
	snap := cloneStatus(st.status)
	m.mu.Unlock()

	m.publish("status", snap)
	if alert != nil {
		m.publish("alert", *alert)
	}
}

func (m *Monitor) Snapshot() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.states))
	for _, st := range m.states {
		out = append(out, cloneStatus(st.status))
	}
	slices.SortFunc(out, func(a, b Status) int { return strings.Compare(a.HostID, b.HostID) })
	return out
}

func (m *Monitor) Alerts() []Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.alerts)
	slices.Reverse(out) // newest first
	if out == nil {
		out = []Alert{}
	}
	return out
}

func cloneStatus(s Status) Status {
	if s.MutedUntil != nil {
		mu := *s.MutedUntil
		s.MutedUntil = &mu
	}
	s.Findings = slices.Clone(s.Findings)
	s.History = slices.Clone(s.History)
	if s.Findings == nil {
		s.Findings = []Finding{}
	}
	if s.History == nil {
		s.History = []Point{}
	}
	return s
}

func errorKind(err error) string {
	var unknown *sshx.UnknownHostError
	var changed *sshx.HostKeyChangedError
	var auth *sshx.AuthRequiredError
	switch {
	case errors.As(err, &unknown):
		return "unknown_host"
	case errors.As(err, &changed):
		return "host_key_changed"
	case errors.As(err, &auth):
		return "auth_required"
	case errors.Is(err, sshx.ErrAuthRejected):
		return "auth_failed"
	case errors.Is(err, sshx.ErrConfig):
		return "config"
	}
	return "network"
}
