package health

import (
	"bufio"
	"context"
	_ "embed"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed metrics.sh
var rawMetricsScript string

// MetricsScript is the live-metrics loop with CRLF stripped (a Windows checkout
// may have rewritten line endings, which sh cannot parse).
var MetricsScript = strings.ReplaceAll(rawMetricsScript, "\r", "")

const (
	liveRingSize   = 120              // last ~4 minutes at the default 2 s tick
	maxLiveStreams = 24               // beyond this, hosts fall back to the 30 s poll
	liveMaxBackoff = 30 * time.Second // retry ceiling after a stream error
)

// Metric is one live sample published on the events hub as {"type":"metric"}.
// CPU is nil until two consecutive proc samples make a percentage defined.
type Metric struct {
	HostID       string   `json:"hostId"`
	At           int64    `json:"at"` // unix seconds
	CPU          *float64 `json:"cpu"`
	Mem          float64  `json:"mem"`
	MemUsedKB    int64    `json:"memUsedKb"`
	MemTotalKB   int64    `json:"memTotalKb"`
	Load1        float64  `json:"load1"`
	Load5        float64  `json:"load5"`
	Load15       float64  `json:"load15"`
	SwapUsedKB   int64    `json:"swapUsedKb"`
	SwapTotalKB  int64    `json:"swapTotalKb"`
	ProcsRunning int      `json:"procsRunning"`
	Cores        int      `json:"cores"`
}

// LivePoint is one entry of a host's high-resolution sparkline ring. Only ticks
// with a defined CPU percentage are kept, so the sparkline has no gaps.
type LivePoint struct {
	T    int64   `json:"t"` // unix seconds
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
	Load float64 `json:"load"`
}

// Streamer is the slice of sshx.Pool the live-metrics loop needs: a session
// multiplexed on the existing pooled connection, and a cheap connected check so
// the loop never dials a host the user has disconnected.
type Streamer interface {
	NewSession(ctx context.Context, hostID string) (*ssh.Session, error)
	Connected(hostID string) bool
}

// metricStreamer runs at most one live-metrics loop per host, bounded by a
// total cap. It is owned by the Monitor, which drives its lifecycle from the
// 30 s poll: a Linux host that is connected and monitored gets a stream;
// everything else is stopped.
type metricStreamer struct {
	ss       Streamer
	script   string
	cap      int
	interval func() time.Duration
	onMetric func(Metric)

	mu      sync.Mutex
	baseCtx context.Context
	cancel  context.CancelFunc
	streams map[string]*liveStream
	active  int
	wg      sync.WaitGroup
}

type liveStream struct {
	cancel context.CancelFunc
}

func newMetricStreamer(ss Streamer, interval func() time.Duration, onMetric func(Metric)) *metricStreamer {
	return &metricStreamer{
		ss:       ss,
		script:   MetricsScript,
		cap:      maxLiveStreams,
		interval: interval,
		onMetric: onMetric,
		streams:  map[string]*liveStream{},
	}
}

// start binds the streamer to a context; every stream is cancelled when it ends
// (app shutdown). It must be called before ensure.
func (s *metricStreamer) start(ctx context.Context) {
	s.mu.Lock()
	s.baseCtx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()
}

// stopAll cancels every running stream and waits for the goroutines to exit, so
// shutdown leaves no leaked goroutine and no remote loop.
func (s *metricStreamer) stopAll() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// ensure starts a stream for the host if one is not already running and the cap
// has room. It is idempotent and safe to call on every poll.
func (s *metricStreamer) ensure(hostID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.baseCtx == nil || s.baseCtx.Err() != nil {
		return
	}
	if _, ok := s.streams[hostID]; ok {
		return
	}
	if s.active >= s.cap {
		return // the 30 s poll still covers this host
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	ls := &liveStream{cancel: cancel}
	s.streams[hostID] = ls
	s.active++
	s.wg.Add(1)
	go s.run(ctx, hostID, ls)
}

// stop ends the host's stream if it has one.
func (s *metricStreamer) stop(hostID string) {
	s.mu.Lock()
	ls := s.streams[hostID]
	s.mu.Unlock()
	if ls != nil {
		ls.cancel()
	}
}

func (s *metricStreamer) activeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *metricStreamer) run(ctx context.Context, hostID string, ls *liveStream) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		if s.streams[hostID] == ls {
			delete(s.streams, hostID)
			s.active--
		}
		s.mu.Unlock()
	}()

	var backoff time.Duration
	for {
		if ctx.Err() != nil {
			return
		}
		// Never dial here: live metrics piggyback on the connection the poll
		// keeps open. If the host is disconnected, exit and let the next
		// successful poll restart us.
		if !s.ss.Connected(hostID) {
			return
		}
		if backoff > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
		lines, _ := s.stream(ctx, hostID)
		if ctx.Err() != nil {
			return
		}
		if lines > 0 {
			// A clean end (the script's max-tick backstop) or a drop after data:
			// restart promptly while the host stays connected.
			backoff = s.interval()
		} else if backoff == 0 {
			backoff = time.Second
		} else {
			backoff = min(backoff*2, liveMaxBackoff)
		}
	}
}

// stream opens one session, runs the loop and scans TWM lines until the session
// ends or the context is cancelled. It returns how many metrics it emitted.
func (s *metricStreamer) stream(ctx context.Context, hostID string) (int, error) {
	sess, err := s.ss.NewSession(ctx, hostID)
	if err != nil {
		return 0, err
	}
	defer sess.Close()

	sess.Stdin = strings.NewReader(s.script)
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return 0, err
	}
	// Cancelling the context (unmonitor, disconnect, shutdown) closes the
	// session, which closes the remote loop's stdout and stops it.
	stop := context.AfterFunc(ctx, func() {
		_ = sess.Signal(ssh.SIGHUP)
		_ = sess.Close()
	})
	defer stop()

	secs := int(s.interval() / time.Second)
	if secs < 1 {
		secs = 1
	}
	if err := sess.Start("sh -s " + strconv.Itoa(secs)); err != nil {
		return 0, err
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 4<<10), 64<<10)
	var (
		prev   twmSample
		havePr bool
		count  int
	)
	for sc.Scan() {
		cur, ok := parseTWM(sc.Text())
		if !ok {
			continue
		}
		m := Metric{
			HostID: hostID, At: cur.at,
			Mem: cur.memPercent(), MemUsedKB: cur.memUsedKB(), MemTotalKB: int64(cur.memTotal),
			Load1: cur.load1, Load5: cur.load5, Load15: cur.load15,
			SwapUsedKB: cur.swapUsedKB(), SwapTotalKB: int64(cur.swapTotal),
			ProcsRunning: cur.procsRunning, Cores: cur.ncpu,
		}
		if havePr {
			if c, ok := cpuBetween(prev, cur); ok {
				m.CPU = &c
			}
		}
		prev, havePr = cur, true
		count++
		s.onMetric(m)
	}
	_ = sess.Wait()
	return count, sc.Err()
}

// twmSample is one parsed TWM line.
type twmSample struct {
	at           int64
	cpuTotal     uint64
	cpuIdle      uint64
	memTotal     uint64
	memAvail     uint64
	load1        float64
	load5        float64
	load15       float64
	swapTotal    uint64
	swapFree     uint64
	procsRunning int
	ncpu         int
}

func (t twmSample) memPercent() float64 {
	if t.memTotal == 0 {
		return 0
	}
	avail := min(t.memAvail, t.memTotal)
	return round1(float64(t.memTotal-avail) / float64(t.memTotal) * 100)
}

func (t twmSample) memUsedKB() int64 {
	if t.memTotal == 0 {
		return 0
	}
	return int64(t.memTotal - min(t.memAvail, t.memTotal))
}

func (t twmSample) swapUsedKB() int64 {
	if t.swapTotal == 0 {
		return 0
	}
	return int64(t.swapTotal - min(t.swapFree, t.swapTotal))
}

// cpuBetween is 100*(Δtotal-Δidle)/Δtotal clamped to [0,100], or ok=false when
// the counters did not advance (or reset, e.g. after a reboot).
func cpuBetween(a, b twmSample) (float64, bool) {
	if b.cpuTotal <= a.cpuTotal || b.cpuIdle < a.cpuIdle {
		return 0, false
	}
	dTotal := b.cpuTotal - a.cpuTotal
	dIdle := b.cpuIdle - a.cpuIdle
	if dIdle > dTotal {
		dIdle = dTotal
	}
	busy := float64(dTotal-dIdle) / float64(dTotal) * 100
	return round1(min(max(busy, 0), 100)), true
}

// parseTWM reads one "TWM ..." line into a sample. Any malformed line is
// ignored so a stray shell message never corrupts the stream.
func parseTWM(line string) (twmSample, bool) {
	f := strings.Fields(line)
	if len(f) != 13 || f[0] != "TWM" {
		return twmSample{}, false
	}
	u := func(s string) (uint64, bool) {
		n, err := strconv.ParseUint(s, 10, 64)
		return n, err == nil
	}
	fl := func(s string) (float64, bool) {
		n, err := strconv.ParseFloat(s, 64)
		return n, err == nil
	}
	var (
		t  twmSample
		ok [13]bool
	)
	var at, ctot, cidle, mt, ma, st, sf uint64
	var l1, l5, l15 float64
	var pr, nc uint64
	at, ok[1] = u(f[1])
	ctot, ok[2] = u(f[2])
	cidle, ok[3] = u(f[3])
	mt, ok[4] = u(f[4])
	ma, ok[5] = u(f[5])
	l1, ok[6] = fl(f[6])
	l5, ok[7] = fl(f[7])
	l15, ok[8] = fl(f[8])
	st, ok[9] = u(f[9])
	sf, ok[10] = u(f[10])
	pr, ok[11] = u(f[11])
	nc, ok[12] = u(f[12])
	for i := 1; i < 13; i++ {
		if !ok[i] {
			return twmSample{}, false
		}
	}
	t = twmSample{
		at: int64(at), cpuTotal: ctot, cpuIdle: cidle,
		memTotal: mt, memAvail: ma,
		load1: l1, load5: l5, load15: l15,
		swapTotal: st, swapFree: sf,
		procsRunning: int(pr), ncpu: int(nc),
	}
	return t, true
}
