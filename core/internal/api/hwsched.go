package api

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/nguyenquocanhz/diagward/model"

	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// Unattended hardware checks: a schedule (Settings.HardwareInterval) and
// "check every server now" runs. Both run Diagward's read-only collector with
// the same limits: at most hwMaxConcurrent checks at a time, never two on one
// host (a running manual check makes them skip that host), and only with root
// or passwordless sudo. No sudo password is ever stored or reused: a host that
// needs one is reported as "sudo_required" until someone checks it by hand or
// configures NOPASSWD.

const (
	hwMaxConcurrent = 2
	hwScheduleFile  = "hardware-schedule.json"
)

type hwSource string

const (
	srcManual   hwSource = "manual"
	srcSchedule hwSource = "schedule"
	srcFleet    hwSource = "fleet"
)

var errHardwareBusy = errors.New("a hardware check is already running on this server")

// hwPlan is what the scheduler remembers about one host. It is saved so a
// restart neither forgets the next run nor checks every host at once.
type hwPlan struct {
	NextRun  time.Time `json:"nextRun,omitzero"`
	LastTry  time.Time `json:"lastTry,omitzero"`   // last unattended attempt
	Error    string    `json:"error,omitempty"`    // code of the last unattended failure
	ErrorMsg string    `json:"errorMsg,omitempty"` // its message (English, technical)
}

// HardwareSummary is the gist of a host's last saved result, small enough
// to send for every host.
type HardwareSummary struct {
	Verdict string `json:"verdict"`
	// Headline mirrors the UI's verdict banner: crit, warn, ok, guest (a
	// healthy VM or container) or none (nothing could be checked).
	Headline string      `json:"headline"`
	SavedAt  time.Time   `json:"savedAt"`
	RanAs    string      `json:"ranAs"`
	Partial  bool        `json:"partial,omitempty"`
	Crit     int         `json:"crit"`
	Warn     int         `json:"warn"`
	Info     int         `json:"info"`
	Top      *model.Text `json:"top,omitempty"` // title of the most severe problem
}

// HardwareHost is the fleet view of one host.
type HardwareHost struct {
	HostID  string           `json:"hostId"`
	Summary *HardwareSummary `json:"summary,omitempty"`
	// State is "queued" or "running" for unattended checks, "manual" while
	// someone runs one by hand, else empty.
	State    string     `json:"state,omitempty"`
	NextRun  *time.Time `json:"nextRun,omitempty"`
	Error    string     `json:"error,omitempty"`
	ErrorMsg string     `json:"errorMsg,omitempty"`
	ErrorAt  *time.Time `json:"errorAt,omitempty"`
}

// HardwareRun is one "check every server" run.
type HardwareRun struct {
	ID         string            `json:"id"`
	Active     bool              `json:"active"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt *time.Time        `json:"finishedAt,omitempty"`
	Hosts      []string          `json:"hosts"`
	Done       int               `json:"done"`
	OK         []string          `json:"ok"`
	NeedsSudo  []string          `json:"needsSudo"`
	Failed     map[string]string `json:"failed"`  // host id -> error code
	Skipped    []string          `json:"skipped"` // cancelled, or busy with a manual check

	pending map[string]bool
}

func (r *HardwareRun) clone() *HardwareRun {
	c := *r
	c.Hosts = slices.Clone(r.Hosts)
	c.OK = nonNilS(slices.Clone(r.OK))
	c.NeedsSudo = nonNilS(slices.Clone(r.NeedsSudo))
	c.Skipped = nonNilS(slices.Clone(r.Skipped))
	c.Failed = make(map[string]string, len(r.Failed))
	for k, v := range r.Failed {
		c.Failed[k] = v
	}
	c.pending = nil
	return &c
}

func nonNilS(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

type hwScheduler struct {
	s *Server
	// check runs one unattended check (no sudo password); tests replace it.
	check func(ctx context.Context, hostID string, src hwSource) error
	now   func() time.Time
	// randDur returns a random duration in [0, d).
	randDur func(d time.Duration) time.Duration
	// override replaces the daily/weekly period when set (development).
	override time.Duration
	sem      chan struct{}
	wake     chan struct{}

	mu        sync.Mutex
	loaded    bool
	plans     map[string]*hwPlan
	summaries map[string]*HardwareSummary // nil value: never checked
	queued    map[string]uint64           // queued or running unattended -> job number
	running   map[string]bool
	jobSeq    uint64
	run       *HardwareRun
	jobs      sync.WaitGroup
}

func newHWScheduler(s *Server) *hwScheduler {
	hs := &hwScheduler{
		s:   s,
		now: time.Now,
		randDur: func(d time.Duration) time.Duration {
			if d <= 0 {
				return 0
			}
			return rand.N(d)
		},
		sem:       make(chan struct{}, hwMaxConcurrent),
		wake:      make(chan struct{}, 1),
		plans:     map[string]*hwPlan{},
		summaries: map[string]*HardwareSummary{},
		queued:    map[string]uint64{},
		running:   map[string]bool{},
	}
	hs.check = hs.unattendedCheck
	return hs
}

// interval is the effective schedule period, 0 when scheduling is off.
func (hs *hwScheduler) interval() time.Duration {
	var d time.Duration
	switch hs.s.store.Settings().HardwareInterval {
	case store.HardwareDaily:
		d = 24 * time.Hour
	case store.HardwareWeekly:
		d = 7 * 24 * time.Hour
	default:
		return 0
	}
	if hs.override > 0 {
		return hs.override
	}
	return d
}

// firstDelay spreads first (and overdue) checks: a short pause, then a
// random share of up to a quarter of the period (at most an hour).
func (hs *hwScheduler) firstDelay(iv time.Duration) time.Duration {
	return min(iv/20, 2*time.Minute) + hs.randDur(min(iv/4, time.Hour))
}

// jitter keeps hosts checked together from staying in lockstep.
func (hs *hwScheduler) jitter(iv time.Duration) time.Duration {
	return hs.randDur(min(iv/10, 30*time.Minute))
}

// retryDelay is when a failed unattended check is tried again.
func retryDelay(code string, iv time.Duration) time.Duration {
	switch code {
	case "sudo_required", "sudo_wrong", "auth_required", "auth_failed", "unknown_host", "host_key_changed", "unsupported":
		return iv // needs a person; try again next period
	case "hardware_busy":
		return min(iv/8, 15*time.Minute)
	}
	return min(iv/6, 4*time.Hour) // network trouble, timeouts
}

// ---------------------------------------------------------------- persistence

func (hs *hwScheduler) path() string { return filepath.Join(hs.s.dataDir, hwScheduleFile) }

func (hs *hwScheduler) loadLocked() {
	if hs.loaded {
		return
	}
	hs.loaded = true
	data, err := os.ReadFile(hs.path())
	if err != nil {
		return
	}
	var plans map[string]*hwPlan
	if json.Unmarshal(data, &plans) != nil {
		return
	}
	now := hs.now()
	iv := hs.interval()
	for id, p := range plans {
		if p == nil || !hostIDRe.MatchString(id) {
			continue
		}
		// Missed while Termward was not running: spread them out again
		// instead of running every overdue host at once.
		if iv > 0 && !p.NextRun.IsZero() && p.NextRun.Before(now) {
			p.NextRun = now.Add(hs.firstDelay(iv))
		}
		hs.plans[id] = p
	}
}

func (hs *hwScheduler) saveLocked() {
	data, err := json.MarshalIndent(hs.plans, "", "  ")
	if err != nil {
		return
	}
	p := hs.path()
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

func (hs *hwScheduler) planLocked(id string) *hwPlan {
	p, ok := hs.plans[id]
	if !ok {
		p = &hwPlan{}
		hs.plans[id] = p
	}
	return p
}

// summaryLocked returns the cached summary, reading the saved result once.
func (hs *hwScheduler) summaryLocked(id string) *HardwareSummary {
	if sum, ok := hs.summaries[id]; ok {
		return sum
	}
	var sum *HardwareSummary
	if res, err := loadHardware(hs.s.dataDir, id); err == nil {
		sum = summarize(res)
	}
	hs.summaries[id] = sum
	return sum
}

// summarize condenses a result; Headline follows the UI's verdict banner.
func summarize(res *HardwareResult) *HardwareSummary {
	r := res.Report
	sum := &HardwareSummary{Verdict: r.Verdict.String(), SavedAt: res.SavedAt, RanAs: res.RanAs, Partial: res.Partial}
	topSev := model.Info
	for _, f := range r.Findings {
		switch f.Severity {
		case model.Crit:
			sum.Crit++
		case model.Warn:
			sum.Warn++
		case model.Info:
			sum.Info++
		}
		if f.Severity >= model.Warn && f.Severity > topSev {
			t := f.Title
			sum.Top, topSev = &t, f.Severity
		}
	}
	checked := slices.ContainsFunc(r.Summary, func(c model.ComponentSummary) bool { return c.Checked })
	switch {
	case r.Verdict == model.Crit || r.Verdict == model.Warn:
		sum.Headline = r.Verdict.String()
	case !checked:
		sum.Headline = "none"
	case r.Env.OS != "" && r.Env.OS != "bmc" && (r.Env.Virtual != "" || r.Env.Container):
		sum.Headline = "guest"
	default:
		sum.Headline = "ok"
	}
	return sum
}

// ---------------------------------------------------------------- views

func (hs *hwScheduler) hostViewLocked(id string) HardwareHost {
	v := HardwareHost{HostID: id, Summary: hs.summaryLocked(id)}
	switch {
	case hs.running[id]:
		v.State = "running"
	case hs.queued[id] != 0:
		v.State = "queued"
	default:
		if src, ok := hs.s.hwBusy.Load(id); ok && src == srcManual {
			v.State = "manual"
		}
	}
	if p, ok := hs.plans[id]; ok {
		if !p.NextRun.IsZero() && hs.interval() > 0 {
			t := p.NextRun
			v.NextRun = &t
		}
		if p.Error != "" {
			v.Error, v.ErrorMsg = p.Error, p.ErrorMsg
			t := p.LastTry
			v.ErrorAt = &t
		}
	}
	return v
}

func (hs *hwScheduler) publishHost(id string) {
	hs.mu.Lock()
	hs.loadLocked()
	v := hs.hostViewLocked(id)
	hs.mu.Unlock()
	hs.s.hub.Publish("hardware_host", v)
}

func (hs *hwScheduler) publishRunLocked() {
	if hs.run != nil {
		hs.s.hub.Publish("hardware_run", hs.run.clone())
	}
}

// fleet is the response of GET /api/hardware.
type fleetView struct {
	Interval string         `json:"interval"`
	Hosts    []HardwareHost `json:"hosts"`
	Run      *HardwareRun   `json:"run,omitempty"`
}

func (hs *hwScheduler) fleet() fleetView {
	hosts := hs.s.store.Hosts()
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.loadLocked()
	v := fleetView{Interval: hs.s.store.Settings().HardwareInterval, Hosts: make([]HardwareHost, 0, len(hosts))}
	for _, h := range hosts {
		v.Hosts = append(v.Hosts, hs.hostViewLocked(h.ID))
	}
	if hs.run != nil {
		v.Run = hs.run.clone()
	}
	return v
}

// ---------------------------------------------------------------- scheduling

// Run plans and starts scheduled checks until ctx ends.
func (hs *hwScheduler) Run(ctx context.Context) {
	for {
		hs.tick(ctx)
		every := 30 * time.Second
		if iv := hs.interval(); iv > 0 {
			every = min(max(iv/10, time.Second), every)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		case <-hs.wake:
		}
	}
}

// Wake re-plans now (settings or hosts changed).
func (hs *hwScheduler) Wake() {
	select {
	case hs.wake <- struct{}{}:
	default:
	}
}

// tick assigns next-run times to monitored hosts that have none and starts
// the checks that are due.
func (hs *hwScheduler) tick(ctx context.Context) {
	hosts := hs.s.store.Hosts()
	iv := hs.interval()
	now := hs.now()

	hs.mu.Lock()
	hs.loadLocked()
	changed := false
	known := map[string]bool{}
	var due []string
	for _, h := range hosts {
		known[h.ID] = true
		p := hs.planLocked(h.ID)
		if iv <= 0 || !h.Monitor {
			if !p.NextRun.IsZero() {
				p.NextRun, changed = time.Time{}, true
			}
			continue
		}
		switch {
		case p.NextRun.IsZero():
			next := now.Add(hs.firstDelay(iv))
			if sum := hs.summaryLocked(h.ID); sum != nil {
				// Checked before (by hand or before a restart): one period
				// after that, unless that is already past.
				if t := sum.SavedAt.Add(iv + hs.jitter(iv)); t.After(next) {
					next = t
				}
			}
			p.NextRun, changed = next, true
		case p.NextRun.After(now.Add(iv + min(iv/10, 30*time.Minute))):
			// The period was shortened (weekly -> daily).
			p.NextRun, changed = now.Add(hs.firstDelay(iv)), true
		case !p.NextRun.After(now) && hs.queued[h.ID] == 0:
			due = append(due, h.ID)
		}
	}
	for id := range hs.plans {
		if !known[id] {
			delete(hs.plans, id)
			delete(hs.summaries, id)
			changed = true
		}
	}
	for _, id := range due {
		hs.enqueueLocked(ctx, id, srcSchedule)
	}
	if changed {
		hs.saveLocked()
	}
	hs.mu.Unlock()
	for _, id := range due {
		hs.publishHost(id)
	}
}

// enqueueLocked starts a goroutine that waits for a free slot and runs the
// check. A host is queued at most once.
func (hs *hwScheduler) enqueueLocked(ctx context.Context, id string, src hwSource) bool {
	if hs.queued[id] != 0 {
		return false
	}
	hs.jobSeq++
	hs.queued[id] = hs.jobSeq
	hs.jobs.Add(1)
	go hs.job(ctx, id, src, hs.jobSeq)
	return true
}

func (hs *hwScheduler) job(ctx context.Context, id string, src hwSource, seq uint64) {
	defer hs.jobs.Done()
	select {
	case hs.sem <- struct{}{}:
	case <-ctx.Done():
		hs.mu.Lock()
		if hs.queued[id] == seq {
			delete(hs.queued, id)
		}
		hs.mu.Unlock()
		return
	}
	defer func() { <-hs.sem }()

	hs.mu.Lock()
	if hs.queued[id] != seq { // cancelled while waiting (maybe queued again since)
		hs.mu.Unlock()
		return
	}
	// Checked (by hand) or scheduling turned off while this scheduled check
	// waited for a slot: it is not due any more, unless a fleet run is
	// waiting for its result.
	if p := hs.plans[id]; src == srcSchedule && (hs.interval() <= 0 || p != nil && p.NextRun.After(hs.now())) &&
		(hs.run == nil || !hs.run.Active || !hs.run.pending[id]) {
		delete(hs.queued, id)
		hs.mu.Unlock()
		hs.publishHost(id)
		return
	}
	hs.running[id] = true
	hs.mu.Unlock()
	hs.publishHost(id)

	err := hs.check(ctx, id, src)

	hs.mu.Lock()
	delete(hs.running, id)
	delete(hs.queued, id)
	// Termward is quitting: the check was cut off, the server did not fail.
	// The plan stays as it was (overdue hosts are spread again on start).
	if err != nil && ctx.Err() == nil {
		hs.failedLocked(id, src, err)
	}
	hs.finishRunLocked(id, err)
	hs.mu.Unlock()
	hs.publishHost(id)
}

// unattendedCheck is the real check run by the schedule and fleet runs.
func (hs *hwScheduler) unattendedCheck(ctx context.Context, hostID string, src hwSource) error {
	h, err := hs.s.store.Host(hostID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, hardwareTimeout)
	defer cancel()
	_, err = hs.s.runCheck(ctx, h, hardwareInput{}, src)
	return err
}

// recorded updates the cache and the plan after any successful check.
func (hs *hwScheduler) recorded(id string, res *HardwareResult, src hwSource) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.loadLocked()
	hs.summaries[id] = summarize(res)
	p := hs.planLocked(id)
	if src != srcManual {
		p.LastTry = hs.now()
	}
	// A successful check by hand clears an unattended failure too: the host
	// is fine to check, at least with the password that was typed.
	p.Error, p.ErrorMsg = "", ""
	if iv := hs.interval(); iv > 0 {
		p.NextRun = res.SavedAt.Add(iv + hs.jitter(iv))
	}
	hs.saveLocked()
}

// failedLocked records an unattended failure and when to try again. Manual
// failures do not change the plan.
func (hs *hwScheduler) failedLocked(id string, src hwSource, err error) {
	if src == srcManual {
		return
	}
	code := hwErrorCode(err)
	p := hs.planLocked(id)
	p.LastTry = hs.now()
	if code != "hardware_busy" {
		p.Error, p.ErrorMsg = code, err.Error()
	}
	if iv := hs.interval(); iv > 0 {
		p.NextRun = hs.now().Add(retryDelay(code, iv) + hs.jitter(iv))
	}
	hs.saveLocked()
}

func (hs *hwScheduler) forget(id string) {
	hs.mu.Lock()
	hs.loadLocked() // or saving would drop every other host's plan
	delete(hs.plans, id)
	delete(hs.summaries, id)
	hs.saveLocked()
	hs.mu.Unlock()
}

// ---------------------------------------------------------------- fleet runs

func (hs *hwScheduler) finishRunLocked(id string, err error) {
	r := hs.run
	if r == nil || !r.Active || !r.pending[id] {
		return
	}
	delete(r.pending, id)
	r.Done++
	switch code := hwErrorCode(err); {
	case err == nil:
		r.OK = append(r.OK, id)
	case code == "sudo_required" || code == "sudo_wrong":
		r.NeedsSudo = append(r.NeedsSudo, id)
	case code == "hardware_busy" || errors.Is(err, context.Canceled):
		r.Skipped = append(r.Skipped, id)
	default:
		r.Failed[id] = code
	}
	hs.endRunIfDoneLocked()
	hs.publishRunLocked()
}

func (hs *hwScheduler) endRunIfDoneLocked() {
	if r := hs.run; r != nil && r.Active && len(r.pending) == 0 {
		r.Active = false
		t := hs.now().UTC()
		r.FinishedAt = &t
	}
}

// startRun queues a check of every given host (default: every monitored
// host). Hosts already queued by the schedule join the run.
func (hs *hwScheduler) startRun(ctx context.Context, ids []string) (*HardwareRun, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.run != nil && hs.run.Active {
		return hs.run.clone(), errRunActive
	}
	r := &HardwareRun{
		ID: store.NewID(), Active: true, StartedAt: hs.now().UTC(),
		Hosts: ids, Failed: map[string]string{}, pending: map[string]bool{},
	}
	hs.run = r
	for _, id := range ids {
		r.pending[id] = true
		hs.enqueueLocked(ctx, id, srcFleet)
	}
	hs.publishRunLocked()
	return r.clone(), nil
}

var errRunActive = errors.New("a check of every server is already running")

// cancelRun drops the hosts still waiting; running checks finish.
func (hs *hwScheduler) cancelRun() *HardwareRun {
	hs.mu.Lock()
	var dropped []string
	if r := hs.run; r != nil && r.Active {
		for id := range r.pending {
			if hs.running[id] {
				continue
			}
			delete(r.pending, id)
			delete(hs.queued, id)
			r.Skipped = append(r.Skipped, id)
			r.Done++
			dropped = append(dropped, id)
		}
		hs.endRunIfDoneLocked()
		hs.publishRunLocked()
	}
	var out *HardwareRun
	if hs.run != nil {
		out = hs.run.clone()
	}
	hs.mu.Unlock()
	for _, id := range dropped {
		hs.publishHost(id)
	}
	return out
}

// ---------------------------------------------------------------- handlers

func (s *Server) hardwareFleet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.hw.fleet())
}

func (s *Server) hardwareRunAll(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostIDs []string `json:"hostIds"`
	}
	if !decodeOptional(w, r, &in) {
		return
	}
	var ids []string
	if len(in.HostIDs) > 0 {
		seen := map[string]bool{}
		for _, id := range in.HostIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			if _, err := s.store.Host(id); err != nil {
				fail(w, err, http.StatusBadRequest)
				return
			}
			ids = append(ids, id)
		}
	} else {
		for _, h := range s.store.Hosts() {
			if h.Monitor {
				ids = append(ids, h.ID)
			}
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "invalid", "no monitored servers to check", nil)
		return
	}
	run, err := s.hw.startRun(s.ctx, ids)
	if errors.Is(err, errRunActive) {
		writeError(w, http.StatusConflict, "run_active", err.Error(), run)
		return
	}
	for _, id := range ids {
		s.hw.publishHost(id)
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) hardwareRunCancel(w http.ResponseWriter, _ *http.Request) {
	run := s.hw.cancelRun()
	if run == nil {
		writeError(w, http.StatusNotFound, "not_found", "no check of every server has run", nil)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// RunHardwareScheduler runs scheduled hardware checks until ctx ends.
func (s *Server) RunHardwareScheduler(ctx context.Context) { s.hw.Run(ctx) }

// alertHardware raises alerts for what changed since the previous check.
func (s *Server) alertHardware(h store.Host, prev, cur *HardwareResult) []health.Alert {
	var raised []health.Alert
	for _, a := range hardwareAlerts(h.ID, h.Name, prev, diffHardware(prev, cur)) {
		if got, ok := s.monitor.Raise(a); ok {
			raised = append(raised, got)
		}
	}
	return raised
}
