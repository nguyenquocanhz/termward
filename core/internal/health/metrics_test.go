package health

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// ---------------------------------------------------------- pure unit tests

func TestParseTWM(t *testing.T) {
	line := "TWM 1700000000 1190 890 8000000 2000000 0.52 0.61 0.58 2000000 1500000 3 4"
	s, ok := parseTWM(line)
	if !ok {
		t.Fatal("well-formed line rejected")
	}
	if s.at != 1700000000 || s.cpuTotal != 1190 || s.cpuIdle != 890 || s.ncpu != 4 || s.procsRunning != 3 {
		t.Fatalf("fields: %+v", s)
	}
	if s.load1 != 0.52 || s.load5 != 0.61 || s.load15 != 0.58 {
		t.Fatalf("load: %+v", s)
	}
	if s.memPercent() != 75 || s.memUsedKB() != 6000000 {
		t.Fatalf("mem = %v%% used %d", s.memPercent(), s.memUsedKB())
	}
	if s.swapUsedKB() != 500000 {
		t.Fatalf("swap used = %d", s.swapUsedKB())
	}

	for _, bad := range []string{
		"", "hello world", "TWM 1 2 3", "noise TWM 1 2 3 4 5 6 7 8 9 10 11",
		"TWM x 2 3 4 5 6 7 8 9 10 11 12", // non-numeric epoch
	} {
		if _, ok := parseTWM(bad); ok {
			t.Errorf("malformed line accepted: %q", bad)
		}
	}
}

func TestCPUBetween(t *testing.T) {
	a := twmSample{cpuTotal: 1000, cpuIdle: 800}
	b := twmSample{cpuTotal: 1190, cpuIdle: 890} // Δtotal=190 Δidle=90 → 52.6%
	if c, ok := cpuBetween(a, b); !ok || c != 52.6 {
		t.Fatalf("cpuBetween = %v ok=%v, want 52.6", c, ok)
	}
	// No advance (identical counters) or a reset must be undefined.
	if _, ok := cpuBetween(a, a); ok {
		t.Error("no delta should be undefined")
	}
	if _, ok := cpuBetween(b, a); ok {
		t.Error("counter reset should be undefined")
	}
	// A fully busy tick clamps to 100.
	busy := twmSample{cpuTotal: 1100, cpuIdle: 800}
	if c, ok := cpuBetween(a, busy); !ok || c != 100 {
		t.Fatalf("all-busy = %v ok=%v, want 100", c, ok)
	}
}

func TestSampleIsLinux(t *testing.T) {
	if !sampleIsLinux(&Sample{MemTotalKB: 8000000, Kernel: "6.8.0"}) {
		t.Error("a /proc-derived sample should be Linux")
	}
	if sampleIsLinux(nil) {
		t.Error("nil sample is not Linux")
	}
	if sampleIsLinux(&Sample{OS: "Windows"}) {
		t.Error("a Windows host (no /proc fields) must not be Linux")
	}
}

// ---------------------------------------------------------- SSH test server

// metricServer is an in-process SSH server. For the collector poll ("sh -s")
// it returns a canned sample; for the live loop ("sh -s <n>") it streams TWM
// lines until the client closes the channel, which it records.
type metricServer struct {
	addr    string
	port    int
	hostKey ssh.Signer
	sample  string
	lines   []string
	gap     time.Duration

	mu         sync.Mutex
	liveOpened int
	liveClosed int
}

func (ms *metricServer) stats() (opened, closed int) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.liveOpened, ms.liveClosed
}

func startMetricServer(t *testing.T, allow ssh.PublicKey, sample string, lines []string) *metricServer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	hostKey, _ := ssh.NewSignerFromKey(priv)
	ms := &metricServer{sample: sample, lines: lines, gap: 10 * time.Millisecond, hostKey: hostKey}
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, nil // any key the client offers is fine for the test
	}}
	cfg.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go ms.serve(conn, cfg)
		}
	}()
	ms.addr = "127.0.0.1"
	ms.port = ln.Addr().(*net.TCPAddr).Port
	return ms
}

func (ms *metricServer) serve(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go ms.session(ch, creqs)
	}
}

func (ms *metricServer) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
		var p struct{ Cmd string }
		ssh.Unmarshal(req.Payload, &p)
		req.Reply(true, nil)

		switch {
		case p.Cmd == "sh -s": // the 30 s collector
			io.Copy(io.Discard, ch) // drain the script until stdin EOF
			io.WriteString(ch, ms.sample)
			sendExit(ch, 0)
			ch.Close()
		case strings.HasPrefix(p.Cmd, "sh -s "): // the live loop
			ms.mu.Lock()
			ms.liveOpened++
			ms.mu.Unlock()
			go io.Copy(io.Discard, ch) // drain the loop script on stdin
			ms.streamLines(ch)
		default:
			ch.Close()
		}
		return
	}
}

func (ms *metricServer) streamLines(ch ssh.Channel) {
	last := "TWM 0 0 0 0 0 0 0 0 0 0 0 0"
	for i := 0; ; i++ {
		ln := last
		if i < len(ms.lines) {
			ln = ms.lines[i]
			last = ln
		}
		if _, err := io.WriteString(ch, ln+"\n"); err != nil {
			ms.mu.Lock()
			ms.liveClosed++ // client hung up: the loop stops cleanly
			ms.mu.Unlock()
			return
		}
		time.Sleep(ms.gap)
	}
}

func sendExit(ch ssh.Channel, code int) {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(code))
	ch.SendRequest("exit-status", false, b)
}

// ---------------------------------------------------------- fixture

type metricFixture struct {
	store *store.Store
	pool  *sshx.Pool
	key   store.Key
}

func newMetricFixture(t *testing.T) *metricFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sec := secret.NewWithBackend(secret.NewMemory())
	km, err := keys.NewManager(filepath.Join(dir, "keys"), st, sec)
	if err != nil {
		t.Fatal(err)
	}
	k, err := km.Generate(keys.GenerateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	known, err := sshx.NewKnownHosts(filepath.Join(dir, "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	p := sshx.NewPool(st, km, sec, known)
	t.Cleanup(p.CloseAll)
	return &metricFixture{store: st, pool: p, key: k}
}

func (f *metricFixture) pub(t *testing.T) ssh.PublicKey {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(f.key.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// addHost registers a host for the server and trusts its key (first-use).
func (f *metricFixture) addHost(t *testing.T, ms *metricServer, monitor bool) store.Host {
	t.Helper()
	h, err := f.store.SaveHost(store.Host{
		Name: "srv", Address: ms.addr, Port: ms.port, User: "tester",
		Auth: store.AuthKey, KeyID: f.key.ID, Monitor: monitor,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = f.pool.Client(ctx, h.ID)
	var unknown *sshx.UnknownHostError
	switch {
	case errors.As(err, &unknown): // first use: trust the key
		if err := f.pool.Known().Trust(unknown.HostID, unknown.Fingerprint); err != nil {
			t.Fatal(err)
		}
	case err != nil:
		t.Fatalf("connect: %v", err)
	}
	return h
}

// ---------------------------------------------------------- streamer tests

const (
	twmLine1 = "TWM 1000 1000 800 8000000 2000000 0.5 0.6 0.7 2000000 1500000 2 4"
	// Δtotal=190 Δidle=90 → cpu 52.6%
	twmLine2 = "TWM 1002 1190 890 8000000 2000000 0.5 0.6 0.7 2000000 1500000 2 4"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestStreamerComputesMetrics feeds two synthetic TWM lines and checks the
// cpu/mem/swap math, that no CPU is emitted before the second sample, and that
// a metric is published per tick.
func TestStreamerComputesMetrics(t *testing.T) {
	f := newMetricFixture(t)
	ms := startMetricServer(t, f.pub(t), "", []string{twmLine1, twmLine2})
	h := f.addHost(t, ms, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := f.pool.Client(ctx, h.ID); err != nil {
		t.Fatal(err)
	}

	metrics := make(chan Metric, 32)
	s := newMetricStreamer(f.pool, func() time.Duration { return time.Second }, func(m Metric) { metrics <- m })
	s.start(ctx)
	s.ensure(h.ID)
	defer s.stopAll()

	first := <-metrics
	if first.CPU != nil {
		t.Errorf("first tick must have no CPU, got %v", *first.CPU)
	}
	if first.Mem != 75 || first.MemUsedKB != 6000000 || first.MemTotalKB != 8000000 {
		t.Errorf("mem: %+v", first)
	}
	if first.SwapUsedKB != 500000 || first.SwapTotalKB != 2000000 {
		t.Errorf("swap: %+v", first)
	}
	if first.Load1 != 0.5 || first.Cores != 4 || first.ProcsRunning != 2 {
		t.Errorf("fast fields: %+v", first)
	}

	second := <-metrics
	if second.CPU == nil || *second.CPU != 52.6 {
		t.Errorf("second tick CPU = %v, want 52.6", second.CPU)
	}
}

// TestStreamerStopLeavesNoGoroutine checks the lifecycle: a stream starts, then
// stop() ends it, the remote loop is torn down (the server sees the channel
// close), and no goroutine is left running.
func TestStreamerStopLeavesNoGoroutine(t *testing.T) {
	f := newMetricFixture(t)
	ms := startMetricServer(t, f.pub(t), "", []string{twmLine1, twmLine2})
	h := f.addHost(t, ms, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := f.pool.Client(ctx, h.ID); err != nil {
		t.Fatal(err)
	}

	s := newMetricStreamer(f.pool, func() time.Duration { return time.Second }, func(Metric) {})
	s.start(ctx)
	s.ensure(h.ID)
	s.ensure(h.ID) // idempotent: still one stream
	waitFor(t, "stream to open", func() bool { o, _ := ms.stats(); return o == 1 && s.activeCount() == 1 })

	s.stop(h.ID)
	waitFor(t, "stream to stop", func() bool { return s.activeCount() == 0 })
	waitFor(t, "remote loop teardown", func() bool { _, c := ms.stats(); return c == 1 })

	// stopAll on an already-empty streamer must return promptly (no leak).
	done := make(chan struct{})
	go func() { s.stopAll(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopAll leaked: did not return")
	}
}

// TestStreamerConcurrencyCap: with the cap below the host count, only `cap`
// streams run; the rest fall back to the poll.
func TestStreamerConcurrencyCap(t *testing.T) {
	f := newMetricFixture(t)
	ms := startMetricServer(t, f.pub(t), "", []string{twmLine1, twmLine2})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var ids []string
	for range 5 {
		h := f.addHost(t, ms, false)
		if _, err := f.pool.Client(ctx, h.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, h.ID)
	}

	s := newMetricStreamer(f.pool, func() time.Duration { return time.Second }, func(Metric) {})
	s.cap = 3
	s.start(ctx)
	defer s.stopAll()
	for _, id := range ids {
		s.ensure(id)
	}
	waitFor(t, "cap to fill", func() bool { return s.activeCount() == 3 })
	// Give any over-cap stream a chance to (wrongly) start.
	time.Sleep(50 * time.Millisecond)
	if n := s.activeCount(); n != 3 {
		t.Fatalf("active streams = %d, want 3 (cap)", n)
	}
}

// TestStreamerExitsWhenDisconnected: the loop must never re-dial; if the host
// is not connected it stops and lets the poll restart it.
func TestStreamerExitsWhenDisconnected(t *testing.T) {
	f := newMetricFixture(t)
	ms := startMetricServer(t, f.pub(t), "", []string{twmLine1, twmLine2})
	h := f.addHost(t, ms, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := newMetricStreamer(f.pool, func() time.Duration { return 50 * time.Millisecond }, func(Metric) {})
	s.start(ctx)
	defer s.stopAll()
	// Host is not connected (never dialed): ensure starts a goroutine that must
	// exit immediately without dialing.
	s.ensure(h.ID)
	waitFor(t, "stream to exit without connection", func() bool { return s.activeCount() == 0 })
	if o, _ := ms.stats(); o != 0 {
		t.Fatalf("stream dialed a disconnected host: %d live sessions", o)
	}
}

// ---------------------------------------------------------- monitor integration

const linuxSample = "os=Ubuntu 24.04.5 LTS\nkernel=6.8.0-85-generic\nhostname=web\nnproc=4\nload=0.5 0.6 0.7\nmem.MemTotal=8000000\nmem.MemAvailable=2000000\nmem.SwapTotal=2000000\nmem.SwapFree=1500000\n"

// metricRecorder collects published events for assertions.
type metricRecorder struct {
	mu      sync.Mutex
	metrics []Metric
}

func (r *metricRecorder) publish(kind string, v any) {
	if kind != "metric" {
		return
	}
	r.mu.Lock()
	r.metrics = append(r.metrics, v.(Metric))
	r.mu.Unlock()
}

func (r *metricRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.metrics)
}

// TestMonitorStreamsLinuxHost: a connected, monitored Linux host gets a live
// stream after a poll; metrics reach the hub and the live ring on /api/status;
// an unmonitored host stops streaming.
func TestMonitorStreamsLinuxHost(t *testing.T) {
	f := newMetricFixture(t)
	ms := startMetricServer(t, f.pub(t), linuxSample, []string{twmLine1, twmLine2})
	h := f.addHost(t, ms, true)

	rec := &metricRecorder{}
	m := NewMonitor(f.store, f.pool, rec.publish)
	if m.stream == nil {
		t.Fatal("real pool should enable the live streamer")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.stream.start(ctx)
	defer m.stream.stopAll()

	m.Poll(ctx, h.ID)
	if s := m.Snapshot()[0]; !sampleIsLinux(s.Sample) {
		t.Fatalf("poll did not produce a Linux sample: %+v", s.Sample)
	}
	waitFor(t, "live stream to start", func() bool { return m.stream.activeCount() == 1 })
	waitFor(t, "metrics on the hub", func() bool { return rec.count() >= 2 })
	waitFor(t, "live ring to fill", func() bool { return len(m.Snapshot()[0].Live) >= 1 })

	// The sparkline ring carries CPU points (skips the first, CPU-less tick).
	lp := m.Snapshot()[0].Live[0]
	if lp.CPU != 52.6 || lp.Mem != 75 || lp.Load != 0.5 {
		t.Fatalf("live point = %+v, want cpu 52.6 mem 75 load 0.5", lp)
	}
	// The live stream refreshed the fast display fields on the last Sample.
	if s := m.Snapshot()[0].Sample; s == nil || s.CPUPercent != 52.6 || s.MemPercent != 75 {
		t.Fatalf("fast fields not refreshed: %+v", s)
	}

	// Unmonitoring the host (prune) stops the stream cleanly.
	if _, err := f.store.SaveHost(func() store.Host { x, _ := f.store.Host(h.ID); x.Monitor = false; return x }()); err != nil {
		t.Fatal(err)
	}
	m.pollAll(ctx)
	waitFor(t, "stream to stop on unmonitor", func() bool { return m.stream.activeCount() == 0 })
	waitFor(t, "remote loop teardown", func() bool { _, c := ms.stats(); return c >= 1 })
}

// TestMonitorSkipsWindowsHost: a host whose poll yields no /proc data (Windows)
// is never streamed; it stays on the 30 s poll only.
func TestMonitorSkipsWindowsHost(t *testing.T) {
	f := newMetricFixture(t)
	ms := startMetricServer(t, f.pub(t), "os=Windows\n", []string{twmLine1, twmLine2})
	h := f.addHost(t, ms, true)

	m := NewMonitor(f.store, f.pool, func(string, any) {})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.stream.start(ctx)
	defer m.stream.stopAll()

	m.Poll(ctx, h.ID)
	// Give any (wrongly started) stream time to open a session.
	time.Sleep(100 * time.Millisecond)
	if n := m.stream.activeCount(); n != 0 {
		t.Fatalf("Windows host started %d live streams, want 0", n)
	}
	if o, _ := ms.stats(); o != 0 {
		t.Fatalf("Windows host opened %d live sessions, want 0", o)
	}
}
