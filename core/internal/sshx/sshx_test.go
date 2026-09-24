package sshx

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// testServer is a tiny in-process SSH server: it runs "exec" requests through
// a handler and supports direct-tcpip so it can act as a jump host.
type testServer struct {
	addr    string
	port    int
	hostKey ssh.Signer
}

func startServer(t *testing.T, allowKey ssh.PublicKey, password string) *testServer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	hostKey, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if allowKey != nil && bytes.Equal(k.Marshal(), allowKey.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PasswordCallback: func(_ ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
			if password != "" && string(p) == password {
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
			go serveConn(conn, cfg)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	return &testServer{addr: "127.0.0.1", port: port, hostKey: hostKey}
}

func serveConn(conn net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			ch, reqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go serveSession(ch, reqs)
		case "direct-tcpip":
			var p struct {
				Host     string
				Port     uint32
				OrigHost string
				OrigPort uint32
			}
			if err := ssh.Unmarshal(nc.ExtraData(), &p); err != nil {
				nc.Reject(ssh.ConnectionFailed, "bad payload")
				continue
			}
			target, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
			if err != nil {
				nc.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			ch, reqs, err := nc.Accept()
			if err != nil {
				target.Close()
				continue
			}
			go ssh.DiscardRequests(reqs)
			go func() { io.Copy(ch, target); ch.Close() }()
			go func() { io.Copy(target, ch); target.Close() }()
		default:
			nc.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
}

func serveSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
		var p struct{ Cmd string }
		ssh.Unmarshal(req.Payload, &p)
		req.Reply(true, nil)

		var out string
		code := 0
		switch {
		case p.Cmd == "sh -s":
			in, _ := io.ReadAll(ch)
			out = "stdin-bytes=" + strconv.Itoa(len(in)) + "\n"
		case strings.HasPrefix(p.Cmd, "exit "):
			code, _ = strconv.Atoi(strings.TrimPrefix(p.Cmd, "exit "))
		case strings.HasPrefix(p.Cmd, "echo "):
			out = strings.TrimPrefix(p.Cmd, "echo ") + "\n"
		}
		io.WriteString(ch, out)
		status := make([]byte, 4)
		binary.BigEndian.PutUint32(status, uint32(code))
		ch.SendRequest("exit-status", false, status)
		return
	}
}

type fixture struct {
	store *store.Store
	keys  *keys.Manager
	sec   *secret.Store
	pool  *Pool
	key   store.Key
}

func newFixture(t *testing.T) *fixture {
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
	known, err := NewKnownHosts(filepath.Join(dir, "known_hosts"))
	if err != nil {
		t.Fatal(err)
	}
	p := NewPool(st, km, sec, known)
	t.Cleanup(p.CloseAll)
	return &fixture{store: st, keys: km, sec: sec, pool: p, key: k}
}

func (f *fixture) pub(t *testing.T) ssh.PublicKey {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(f.key.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func (f *fixture) addHost(t *testing.T, srv *testServer, mut func(*store.Host)) store.Host {
	t.Helper()
	h := store.Host{Name: "srv", Address: srv.addr, Port: srv.port, User: "tester", Auth: store.AuthKey, KeyID: f.key.ID}
	if mut != nil {
		mut(&h)
	}
	h, err := f.store.SaveHost(h)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestTrustOnFirstUseThenRun(t *testing.T) {
	f := newFixture(t)
	srv := startServer(t, f.pub(t), "")
	h := f.addHost(t, srv, nil)

	_, err := f.pool.Client(ctx(t), h.ID)
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("first connect: want UnknownHostError, got %v", err)
	}
	if unknown.Fingerprint != ssh.FingerprintSHA256(srv.hostKey.PublicKey()) {
		t.Fatalf("fingerprint mismatch: %s", unknown.Fingerprint)
	}
	if err := f.pool.Known().Trust(h.ID, "SHA256:wrong"); err == nil {
		t.Fatal("trusting a different fingerprint must fail")
	}
	if err := f.pool.Known().Trust(h.ID, unknown.Fingerprint); err != nil {
		t.Fatal(err)
	}

	res, err := f.pool.Run(ctx(t), h.ID, "echo hello", nil)
	if err != nil || res.Stdout != "hello\n" || res.ExitCode != 0 {
		t.Fatalf("run: %+v %v", res, err)
	}
	res, err = f.pool.Run(ctx(t), h.ID, "exit 3", nil)
	if err != nil || res.ExitCode != 3 {
		t.Fatalf("exit code: %+v %v", res, err)
	}
	res, err = f.pool.Run(ctx(t), h.ID, "sh -s", strings.NewReader("abcdef"))
	if err != nil || res.Stdout != "stdin-bytes=6\n" {
		t.Fatalf("stdin: %+v %v", res, err)
	}
	if !f.pool.Connected(h.ID) {
		t.Error("connection should be pooled")
	}
}

func TestHostKeyChangeIsDetected(t *testing.T) {
	f := newFixture(t)
	srv := startServer(t, f.pub(t), "")
	h := f.addHost(t, srv, nil)
	trust(t, f, h.ID)

	// The same address now presents a different key (re-installed or spoofed).
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	impostor, _ := ssh.NewSignerFromKey(priv)
	addr := net.JoinHostPort(srv.addr, strconv.Itoa(srv.port))
	remote := &net.TCPAddr{IP: net.ParseIP(srv.addr), Port: srv.port}
	cb := f.pool.Known().Callback(h.ID)

	var changed *HostKeyChangedError
	if err := cb(addr, remote, impostor.PublicKey()); !errors.As(err, &changed) {
		t.Fatalf("want HostKeyChangedError, got %v", err)
	}
	// Accepting the new key replaces the old entry instead of adding a second one.
	if err := f.pool.Known().Trust(h.ID, changed.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := cb(addr, remote, impostor.PublicKey()); err != nil {
		t.Fatalf("new key should now be trusted: %v", err)
	}
	if err := cb(addr, remote, srv.hostKey.PublicKey()); !errors.As(err, &changed) {
		t.Fatalf("old key should no longer be trusted, got %v", err)
	}
}

func TestPreferKnownHostKeyAlgorithm(t *testing.T) {
	f := newFixture(t)
	srv := startServer(t, f.pub(t), "")
	h := f.addHost(t, srv, nil)
	addr := net.JoinHostPort(srv.addr, strconv.Itoa(srv.port))
	if algos := f.pool.Known().HostKeyAlgorithms(addr); len(algos) != 0 {
		t.Fatalf("unknown host should not restrict algorithms: %v", algos)
	}
	trust(t, f, h.ID)
	if algos := f.pool.Known().HostKeyAlgorithms(addr); len(algos) != 1 || algos[0] != ssh.KeyAlgoED25519 {
		t.Fatalf("algorithms = %v", algos)
	}
}

func TestPasswordAuthAsksAndForgetsWrongPassword(t *testing.T) {
	f := newFixture(t)
	srv := startServer(t, nil, "hunter2")
	h := f.addHost(t, srv, func(h *store.Host) { h.Auth = store.AuthPassword })

	// No password yet: ask before touching the network.
	var need *AuthRequiredError
	if _, err := f.pool.Client(ctx(t), h.ID); !errors.As(err, &need) || need.Kind != "password" || need.Wrong {
		t.Fatalf("want password prompt, got %v", err)
	}
	f.sec.Put(secret.HostPassword(h.ID), "wrong", false)
	trust(t, f, h.ID) // host key is verified before the password is sent
	if _, err := f.pool.Client(ctx(t), h.ID); !errors.As(err, &need) || !need.Wrong {
		t.Fatalf("want wrong-password error, got %v", err)
	}
	if _, err := f.sec.Get(secret.HostPassword(h.ID)); err == nil {
		t.Error("a rejected password must be forgotten")
	}
	f.sec.Put(secret.HostPassword(h.ID), "hunter2", false)
	if _, err := f.pool.Client(ctx(t), h.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedKey(t *testing.T) {
	f := newFixture(t)
	srv := startServer(t, nil, "") // accepts no key
	h := f.addHost(t, srv, nil)
	trust(t, f, h.ID)
	if _, err := f.pool.Client(ctx(t), h.ID); !errors.Is(err, ErrAuthRejected) {
		t.Fatalf("want ErrAuthRejected, got %v", err)
	}
}

func TestJumpHost(t *testing.T) {
	f := newFixture(t)
	bastion := startServer(t, f.pub(t), "")
	target := startServer(t, f.pub(t), "")
	jb := f.addHost(t, bastion, func(h *store.Host) { h.Name = "bastion" })
	tg := f.addHost(t, target, func(h *store.Host) { h.Name = "private"; h.JumpHostID = jb.ID })
	trust(t, f, jb.ID)
	trust(t, f, tg.ID)

	res, err := f.pool.Run(ctx(t), tg.ID, "echo via-jump", nil)
	if err != nil || res.Stdout != "via-jump\n" {
		t.Fatalf("run through jump host: %+v %v", res, err)
	}
	if !f.pool.Connected(jb.ID) {
		t.Error("jump host connection should be pooled and shared")
	}
}

func TestConcurrentCallersShareOneDial(t *testing.T) {
	f := newFixture(t)
	srv := startServer(t, f.pub(t), "")
	h := f.addHost(t, srv, nil)
	trust(t, f, h.ID)

	clients := make(chan *ssh.Client, 8)
	for range 8 {
		go func() {
			c, err := f.pool.Client(ctx(t), h.ID)
			if err != nil {
				t.Error(err)
			}
			clients <- c
		}()
	}
	first := <-clients
	for range 7 {
		if c := <-clients; c != first {
			t.Fatal("concurrent callers got different connections")
		}
	}
}

// trust performs the first-use prompt the way the UI does.
func trust(t *testing.T, f *fixture, hostID string) {
	t.Helper()
	_, err := f.pool.Client(ctx(t), hostID)
	var unknown *UnknownHostError
	if !errors.As(err, &unknown) {
		t.Fatalf("expected unknown host, got %v", err)
	}
	if err := f.pool.Known().Trust(unknown.HostID, unknown.Fingerprint); err != nil {
		t.Fatal(err)
	}
}
