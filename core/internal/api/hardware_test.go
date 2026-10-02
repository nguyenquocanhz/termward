package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// execHandler answers one "exec" request of the fake SSH server.
type execHandler func(cmd string, stdin []byte) (stdout string, code int)

// startFakeSSH is a tiny in-process SSH server that accepts one public key
// and answers exec requests through h (stdin is read to EOF first).
func startFakeSSH(t *testing.T, allow ssh.PublicKey, h execHandler) (addr string, port int) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	hostKey, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(k.Marshal(), allow.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
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
			go func() {
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
					ch, reqs, err := nc.Accept()
					if err != nil {
						continue
					}
					go fakeSession(ch, reqs, h)
				}
			}()
		}
	}()
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
}

func fakeSession(ch ssh.Channel, reqs <-chan *ssh.Request, h execHandler) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
		var p struct{ Cmd string }
		ssh.Unmarshal(req.Payload, &p)
		req.Reply(true, nil)
		in, _ := io.ReadAll(ch)
		out, code := h(p.Cmd, in)
		io.WriteString(ch, out)
		status := make([]byte, 4)
		binary.BigEndian.PutUint32(status, uint32(code))
		ch.SendRequest("exit-status", false, status)
		return
	}
}

type hwEnv struct {
	t    *testing.T
	dir  string
	ts   *httptest.Server
	srv  *Server
	host store.Host
}

// newHWEnv starts the API with one host that points at a fake SSH server
// whose host key is already trusted.
func newHWEnv(t *testing.T, h execHandler) *hwEnv {
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
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	addr, port := startFakeSSH(t, pub, h)
	host, err := st.SaveHost(store.Host{Name: "web 1/prod", Address: addr, Port: port, User: "root", Auth: store.AuthKey, KeyID: k.ID})
	if err != nil {
		t.Fatal(err)
	}
	known, _ := sshx.NewKnownHosts(filepath.Join(dir, "known_hosts"))
	pool := sshx.NewPool(st, km, sec, known)
	t.Cleanup(pool.CloseAll)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var unknown *sshx.UnknownHostError
	if _, err := pool.Client(ctx, host.ID); !errors.As(err, &unknown) {
		t.Fatalf("expected an unknown host key, got %v", err)
	}
	if err := known.Trust(host.ID, unknown.Fingerprint); err != nil {
		t.Fatal(err)
	}
	hub := NewHub()
	mon := health.NewMonitor(st, pool, hub.Publish)
	sctx, scancel := context.WithCancel(context.Background())
	t.Cleanup(scancel)
	srv := New(sctx, Deps{Token: "secret-token", Store: st, Keys: km, Secrets: sec, Pool: pool, Monitor: mon, Hub: hub})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &hwEnv{t: t, dir: dir, ts: ts, srv: srv, host: host}
}

func (e *hwEnv) call(method, path, body string) (*http.Response, string) {
	return call(e.t, e.ts, method, path, "secret-token", body)
}

var boundaryRe = regexp.MustCompile(`(?m)^DW_B='([0-9a-f]+)'$`)

// fakeCollectorOutput is what the collector prints on a minimal Linux box.
func fakeCollectorOutput(script, as string, done bool) string {
	m := boundaryRe.FindStringSubmatch(script)
	if m == nil {
		return "no boundary in script"
	}
	b := m[1]
	sec := func(name, out string) string {
		return "==DW:" + b + ":BEGIN " + name + "\n" + out + "\n==DW:" + b + ":ERR\n\n==DW:" + b + ":END rc=0 ms=3\n"
	}
	uid := "0"
	if as == "user" {
		uid = "1000"
	}
	out := "==TW:" + b + ":AS " + as + "\n" +
		sec("meta.ident", "hostname=web1\nuid="+uid+"\nuser=ops\nkernel=6.1.0\narch=x86_64\nnow=2026-10-02T10:00:00Z\nuptime=1234.5") +
		sec("meta.osrelease", "ID=almalinux\nVERSION_ID=\"9.4\"\nPRETTY_NAME=\"AlmaLinux 9.4 (Seafoam Ocelot)\"") +
		sec("meta.virt", "vm=none\ncontainer=none\nsys_vendor=Dell Inc.\nproduct_name=PowerEdge R740") +
		sec("meta.pm", "dnf=1\nsudo=1\nsystemctl=1")
	if done {
		out += sec("meta.done", "now=2026-10-02T10:01:00Z")
	}
	return out
}

// linuxHandler emulates a Linux server: root when rootOK, otherwise sudo
// with password "hunter2".
func linuxHandler(t *testing.T, rootOK bool) execHandler {
	return func(cmd string, stdin []byte) (string, int) {
		switch cmd {
		case "uname -s":
			return "Linux\n", 0
		case "sh -s":
			s := string(stdin)
			if !strings.Contains(s, "cat >\"$F\" <<'TW_HW_") || !strings.Contains(s, `rm -f "$F"`) {
				t.Errorf("wrapper does not use a private temp file:\n%.400s", s)
			}
			switch {
			case rootOK:
				return fakeCollectorOutput(s, "root", true), 0
			case strings.Contains(s, "PW='hunter2'\n"):
				return fakeCollectorOutput(s, "sudo", true), 0
			case strings.Contains(s, "NOROOT=1\n"):
				return fakeCollectorOutput(s, "user", true), 0
			case strings.Contains(s, "PW=''\n"):
				return "", exitSudoRequired
			default:
				return "", exitSudoWrong
			}
		}
		return "", 127
	}
}

func decodeHW(t *testing.T, body string) hardwareResponse {
	t.Helper()
	var res hardwareResponse
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("decode %v: %.300s", err, body)
	}
	if res.Report == nil {
		t.Fatalf("no report in %.300s", body)
	}
	return res
}

func TestHardwareAsRoot(t *testing.T) {
	e := newHWEnv(t, linuxHandler(t, true))
	events, unsubscribe := e.srv.hub.Subscribe()
	defer unsubscribe()

	res, body := e.call("POST", "/api/hosts/"+e.host.ID+"/hardware", `{}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("check: %d %s", res.StatusCode, body)
	}
	hw := decodeHW(t, body)
	if hw.RanAs != "root" || hw.Partial || hw.SavedAt.IsZero() {
		t.Fatalf("ranAs=%q partial=%v savedAt=%v", hw.RanAs, hw.Partial, hw.SavedAt)
	}
	if hw.Report.Host.Hostname != "web1" || !hw.Report.Env.Root || hw.Report.Env.Distro != "almalinux" {
		t.Fatalf("report host/env: %+v %+v", hw.Report.Host, hw.Report.Env)
	}
	if len(hw.Report.Summary) == 0 || len(hw.Report.Results) == 0 {
		t.Fatalf("report has no summary or coverage: %.400s", body)
	}
	if hw.Parts == nil {
		t.Error("parts must be an array, not null")
	}

	var progress []string
	var doneSeen bool
	for len(events) > 0 {
		var m struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(<-events, &m); err != nil {
			t.Fatal(err)
		}
		switch m.Type {
		case "hardware_progress":
			if m.Data["hostId"] != e.host.ID {
				t.Errorf("progress for %q", m.Data["hostId"])
			}
			progress = append(progress, m.Data["section"].(string))
		case "hardware_done":
			sum, _ := m.Data["summary"].(map[string]any)
			doneSeen = m.Data["verdict"] != "" && m.Data["hostId"] == e.host.ID && m.Data["source"] == "manual" && sum["ranAs"] == "root"
		}
	}
	if strings.Join(progress, ",") != "meta.ident,meta.osrelease,meta.virt,meta.pm,meta.done" || !doneSeen {
		t.Fatalf("events: progress=%v done=%v", progress, doneSeen)
	}

	// The last result is kept.
	res, body = e.call("GET", "/api/hosts/"+e.host.ID+"/hardware", "")
	if res.StatusCode != http.StatusOK || decodeHW(t, body).RanAs != "root" {
		t.Fatalf("last: %d %.200s", res.StatusCode, body)
	}
}

func TestHardwareSudoFlow(t *testing.T) {
	e := newHWEnv(t, linuxHandler(t, false))
	path := "/api/hosts/" + e.host.ID + "/hardware"

	res, body := e.call("POST", path, `{}`)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, `"code":"sudo_required"`) {
		t.Fatalf("no password: %d %s", res.StatusCode, body)
	}
	res, body = e.call("POST", path, `{"sudoPassword":"nope"}`)
	if res.StatusCode != http.StatusForbidden || !strings.Contains(body, `"code":"sudo_wrong"`) {
		t.Fatalf("wrong password: %d %s", res.StatusCode, body)
	}
	res, body = e.call("POST", path, `{"sudoPassword":"hunter2"}`)
	if res.StatusCode != http.StatusOK || decodeHW(t, body).RanAs != "sudo" {
		t.Fatalf("sudo: %d %.300s", res.StatusCode, body)
	}
	res, body = e.call("POST", path, `{"allowNoRoot":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("no root: %d %.300s", res.StatusCode, body)
	}
	if hw := decodeHW(t, body); hw.RanAs != "user" || hw.Report.Env.Root {
		t.Fatalf("no root: ranAs=%q root=%v", hw.RanAs, hw.Report.Env.Root)
	}
	if strings.Contains(body, "hunter2") {
		t.Fatal("the sudo password must never be echoed")
	}
	if b, _ := os.ReadFile(filepath.Join(e.dir, "hardware", e.host.ID+".json")); bytes.Contains(b, []byte("hunter2")) {
		t.Fatal("the sudo password must never be saved")
	}
}

func TestHardwareBusy(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	base := linuxHandler(t, true)
	e := newHWEnv(t, func(cmd string, stdin []byte) (string, int) {
		if cmd == "sh -s" {
			once.Do(func() { close(entered) })
			<-release
		}
		return base(cmd, stdin)
	})
	path := "/api/hosts/" + e.host.ID + "/hardware"

	first := make(chan int, 1)
	go func() {
		res, _ := e.call("POST", path, `{}`)
		first <- res.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first check never reached the server")
	}
	res, body := e.call("POST", path, `{}`)
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, `"code":"hardware_busy"`) {
		t.Fatalf("second check: %d %s", res.StatusCode, body)
	}
	close(release)
	if code := <-first; code != http.StatusOK {
		t.Fatalf("first check: %d", code)
	}
	// Free again once the first one finished.
	if res, body := e.call("POST", path, `{}`); res.StatusCode != http.StatusOK {
		t.Fatalf("third check: %d %s", res.StatusCode, body)
	}
}

func TestHardwareReportDownload(t *testing.T) {
	e := newHWEnv(t, linuxHandler(t, true))
	base := "/api/hosts/" + e.host.ID + "/hardware"
	if res, body := e.call("GET", base+"/report?format=html", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("before any check: %d %s", res.StatusCode, body)
	}
	if res, _ := e.call("GET", base, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("last before any check: %d", res.StatusCode)
	}
	if res, body := e.call("POST", base, `{}`); res.StatusCode != http.StatusOK {
		t.Fatalf("check: %d %s", res.StatusCode, body)
	}
	name := regexp.MustCompile(`^attachment; filename="diagward-web1-\d{8}-\d{4}\.(html|md|json)"$`)
	for _, c := range []struct{ query, ctype, contains string }{
		{"format=html&lang=vi", "text/html; charset=utf-8", "<html"},
		{"format=html&lang=en", "text/html; charset=utf-8", "</html>"},
		{"format=md&lang=vi", "text/markdown; charset=utf-8", "web1"},
		{"format=json", "application/json", `"hostname": "web1"`},
	} {
		res, body := e.call("GET", base+"/report?"+c.query, "")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %.200s", c.query, res.StatusCode, body)
		}
		if got := res.Header.Get("Content-Type"); got != c.ctype {
			t.Errorf("%s: content type %q", c.query, got)
		}
		if cd := res.Header.Get("Content-Disposition"); !name.MatchString(cd) {
			t.Errorf("%s: content disposition %q", c.query, cd)
		}
		if !strings.Contains(body, c.contains) {
			t.Errorf("%s: body lacks %q: %.200s", c.query, c.contains, body)
		}
	}
	if res, _ := e.call("GET", base+"/report?format=pdf", ""); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown format: %d", res.StatusCode)
	}
}

func TestHardwarePersistence(t *testing.T) {
	e := newHWEnv(t, linuxHandler(t, true))
	base := "/api/hosts/" + e.host.ID + "/hardware"
	res, body := e.call("POST", base, `{"sinceDays":3}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("check: %d %s", res.StatusCode, body)
	}
	want := decodeHW(t, body)

	// A fresh core on the same data directory reads it back.
	st, err := store.Open(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	sec := secret.NewWithBackend(secret.NewMemory())
	km, _ := keys.NewManager(filepath.Join(e.dir, "keys"), st, sec)
	known, _ := sshx.NewKnownHosts(filepath.Join(e.dir, "known_hosts"))
	pool := sshx.NewPool(st, km, sec, known)
	hub := NewHub()
	srv2 := New(context.Background(), Deps{Token: "secret-token", Store: st, Keys: km, Secrets: sec, Pool: pool,
		Monitor: health.NewMonitor(st, pool, hub.Publish), Hub: hub})
	ts2 := httptest.NewServer(srv2.Handler())
	defer ts2.Close()
	res, body = call(t, ts2, "GET", base, "secret-token", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reload: %d %s", res.StatusCode, body)
	}
	got := decodeHW(t, body)
	if !got.SavedAt.Equal(want.SavedAt) || got.RanAs != want.RanAs || got.Report.Verdict != want.Report.Verdict ||
		got.Report.Env.SinceDays != 3 || len(got.Report.Findings) != len(want.Report.Findings) {
		t.Fatalf("round trip changed the result:\nwant %+v\ngot  %+v", want.HardwareResult, got.HardwareResult)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "hardware", e.host.ID+".json.tmp")); !os.IsNotExist(err) {
		t.Error("temporary file left behind")
	}

	// Deleting the host deletes its result.
	if res, _ := e.call("DELETE", "/api/hosts/"+e.host.ID, ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete host: %d", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "hardware", e.host.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("result kept after the host was deleted: %v", err)
	}
}

func TestHardwarePartialAndFailures(t *testing.T) {
	mode := "partial"
	e := newHWEnv(t, func(cmd string, stdin []byte) (string, int) {
		if cmd == "uname -s" {
			if mode == "darwin" {
				return "Darwin\n", 0
			}
			return "Linux\n", 0
		}
		switch mode {
		case "partial": // the connection dropped before meta.done
			return fakeCollectorOutput(string(stdin), "root", false), 0
		default:
			return "sh: mktemp: oops\n", 1
		}
	})
	base := "/api/hosts/" + e.host.ID + "/hardware"
	res, body := e.call("POST", base, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("partial: %d %s", res.StatusCode, body)
	}
	hw := decodeHW(t, body)
	if !hw.Partial || len(hw.Report.Notes) == 0 || !strings.Contains(hw.Report.Notes[len(hw.Report.Notes)-1].VI, "dừng sớm") {
		t.Fatalf("partial result not flagged: partial=%v notes=%+v", hw.Partial, hw.Report.Notes)
	}

	mode = "garbage"
	res, body = e.call("POST", base, "")
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(body, `"code":"hardware_failed"`) || !strings.Contains(body, "oops") {
		t.Fatalf("failure: %d %s", res.StatusCode, body)
	}
	mode = "darwin"
	res, body = e.call("POST", base, "")
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(body, "Darwin") {
		t.Fatalf("unsupported OS: %d %s", res.StatusCode, body)
	}
	if res, body := e.call("POST", base, `{"sinceDays":9999}`); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("sinceDays: %d %s", res.StatusCode, body)
	}
	if res, _ := e.call("POST", "/api/hosts/nope/hardware", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown host: %d", res.StatusCode)
	}
}

func TestHardwarePathSafety(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"", ".", "..", "../x", "..\\x", "a/b", "a\\b", "/etc/passwd", "C:\\x", "x.json", "a b", "%2e%2e", strings.Repeat("a", 65)} {
		if p, err := hardwarePath(dir, id); err == nil {
			t.Errorf("id %q accepted: %s", id, p)
		}
	}
	p, err := hardwarePath(dir, "0123abcd_-")
	if err != nil || filepath.Dir(p) != filepath.Join(dir, "hardware") {
		t.Fatalf("valid id: %s %v", p, err)
	}
	if _, err := loadHardware(dir, "../termward"); !os.IsNotExist(err) {
		t.Errorf("load with a traversal id: %v", err)
	}
	removeHardware(dir, "..") // must not touch anything outside

	// Over HTTP, encoded traversal never reaches the file system.
	e := newHWEnv(t, linuxHandler(t, true))
	for _, p := range []string{"/api/hosts/..%2F..%2Ftermward/hardware", "/api/hosts/%2E%2E/hardware/report?format=json"} {
		res, body := e.call("GET", p, "")
		if res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %s", p, res.StatusCode, body)
		}
		if strings.Contains(body, `"version"`) {
			t.Errorf("%s leaked a file: %s", p, body)
		}
	}
}

func TestSafeFileName(t *testing.T) {
	for in, want := range map[string]string{
		"web1":                  "web1",
		"web 1/prod":            "web-1-prod",
		`a"b;c\r\nd`:            "a-b-c-r-nd",
		"../../etc":             "etc",
		"":                      "host",
		"máy-chủ.example.com":   "m-y-ch-.example.com",
		strings.Repeat("x", 99): strings.Repeat("x", 64),
	} {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTakeRanAsAndMarkerWriter(t *testing.T) {
	who, rest := takeRanAs("==TW:b1:AS sudo\n==DW:b1:BEGIN x\n", "b1")
	if who != "sudo" || rest != "==DW:b1:BEGIN x\n" {
		t.Fatalf("takeRanAs: %q %q", who, rest)
	}
	if who, _ := takeRanAs("==TW:other:AS root\n", "b1"); who != "" {
		t.Fatal("a marker with another boundary must be ignored")
	}

	var got []string
	m := &markerWriter{marker: []byte("==DW:b:BEGIN "), progress: func(s string) { got = append(got, s) }, max: 1 << 20}
	stream := "noise\n==DW:b:BEGIN disk.lsblk\nout\n==DW:b:END rc=0\n==DW:b:BEGIN raid.mdstat\n"
	for i := 0; i < len(stream); i += 7 { // arrives in odd-sized chunks
		m.Write([]byte(stream[i:min(i+7, len(stream))]))
	}
	if strings.Join(got, ",") != "disk.lsblk,raid.mdstat" || m.String() != stream {
		t.Fatalf("markers %v, kept %q", got, m.String())
	}
}

// TestLinuxWrapperWithRealShell runs the wrapper under the local sh with a
// stand-in collector that prints its own path.
func TestLinuxWrapperWithRealShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	run := func(script string) (string, int) {
		cmd := exec.Command("sh", "-s")
		cmd.Stdin = strings.NewReader(script)
		out, err := cmd.Output()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return string(out), ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(out), 0
	}
	collector := "echo \"path=$0\"\necho 'quotes '\\'' and TW_HW_ and EOF survive'\n"
	out, code := run(linuxWrapper(collector, "bb", "", true))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	who, rest := takeRanAs(out, "bb")
	if who == "" || !strings.Contains(rest, "quotes ' and TW_HW_ and EOF survive") {
		t.Fatalf("output %q", out)
	}
	path := strings.TrimPrefix(strings.SplitN(rest, "\n", 2)[0], "path=")
	if path == "" {
		t.Fatalf("no path in %q", rest)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary collector %s was not removed: %v", path, err)
	}

	// Without allowNoRoot a plain user gets 77 (root and passwordless sudo
	// run it).
	out, code = run(linuxWrapper(collector, "bb", "", false))
	switch who, _ := takeRanAs(out, "bb"); {
	case code == exitSudoRequired && who == "":
	case code == 0 && (who == "root" || who == "sudo"):
	default:
		t.Fatalf("no root allowed: exit %d, ran as %q", code, who)
	}
}
