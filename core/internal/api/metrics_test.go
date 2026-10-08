package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// liveSSHServer is a minimal in-process SSH server: the collector poll gets a
// Linux sample; the live loop gets a stream of TWM lines until hangup.
type liveSSHServer struct {
	addr, host string
	port       int
}

const (
	liveCollectorSample = "os=Ubuntu 24.04\nkernel=6.8.0-85-generic\nnproc=4\nload=0.5 0.6 0.7\nmem.MemTotal=8000000\nmem.MemAvailable=2000000\nmem.SwapTotal=2000000\nmem.SwapFree=1500000\n"
	liveTWM1            = "TWM 1000 1000 800 8000000 2000000 0.5 0.6 0.7 2000000 1500000 2 4"
	liveTWM2            = "TWM 1002 1190 890 8000000 2000000 0.5 0.6 0.7 2000000 1500000 2 4" // cpu 52.6%
)

func startLiveSSHServer(t *testing.T) *liveSSHServer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	hostKey, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
		return nil, nil
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
			go serveLive(conn, cfg)
		}
	}()
	return &liveSSHServer{addr: "127.0.0.1", host: "127.0.0.1", port: ln.Addr().(*net.TCPAddr).Port}
}

func serveLive(conn net.Conn, cfg *ssh.ServerConfig) {
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
		go func() {
			for req := range creqs {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				var p struct{ Cmd string }
				ssh.Unmarshal(req.Payload, &p)
				req.Reply(true, nil)
				switch {
				case p.Cmd == "sh -s":
					io.Copy(io.Discard, ch)
					io.WriteString(ch, liveCollectorSample)
					st := make([]byte, 4)
					binary.BigEndian.PutUint32(st, 0)
					ch.SendRequest("exit-status", false, st)
					ch.Close()
				case strings.HasPrefix(p.Cmd, "sh -s "):
					go io.Copy(io.Discard, ch)
					lines := []string{liveTWM1, liveTWM2}
					for i := 0; ; i++ {
						ln := liveTWM2
						if i < len(lines) {
							ln = lines[i]
						}
						if _, err := io.WriteString(ch, ln+"\n"); err != nil {
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
				default:
					ch.Close()
				}
				return
			}
		}()
	}
}

// TestStatusLiveRing drives the whole core: the monitor polls a Linux host,
// starts a live stream, and /api/status reports the high-resolution live ring.
func TestStatusLiveRing(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sec := secret.NewWithBackend(secret.NewMemory())
	km, _ := keys.NewManager(filepath.Join(dir, "keys"), st, sec)
	k, err := km.Generate(keys.GenerateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	known, _ := sshx.NewKnownHosts(filepath.Join(dir, "known_hosts"))
	pool := sshx.NewPool(st, km, sec, known)
	t.Cleanup(pool.CloseAll)

	srv := startLiveSSHServer(t)
	h, err := st.SaveHost(store.Host{
		Name: "web", Address: srv.addr, Port: srv.port, User: "tester",
		Auth: store.AuthKey, KeyID: k.ID, Monitor: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Trust the host key (first use).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, cerr := pool.Client(ctx, h.ID)
	var unknown *sshx.UnknownHostError
	if errors.As(cerr, &unknown) {
		if err := known.Trust(unknown.HostID, unknown.Fingerprint); err != nil {
			t.Fatal(err)
		}
	} else if cerr != nil {
		t.Fatalf("connect: %v", cerr)
	}

	hub := NewHub()
	mon := health.NewMonitor(st, pool, hub.Publish)
	apiSrv := New(ctx, Deps{Token: "secret-token", Store: st, Keys: km, Secrets: sec, Pool: pool, Monitor: mon, Hub: hub})
	ts := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(ts.Close)

	go mon.Run(ctx) // immediate first poll starts the live stream

	type statusResp struct {
		Statuses []struct {
			HostID string             `json:"hostId"`
			Live   []health.LivePoint `json:"live"`
		} `json:"statuses"`
		Connected []string `json:"connected"`
	}
	deadline := time.Now().Add(5 * time.Second)
	var sr statusResp
	for time.Now().Before(deadline) {
		res, body := call(t, ts, "GET", "/api/status", "secret-token", "")
		if res.StatusCode != 200 {
			t.Fatalf("status: %d %s", res.StatusCode, body)
		}
		sr = statusResp{}
		if err := json.Unmarshal([]byte(body), &sr); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(sr.Statuses) == 1 && len(sr.Statuses[0].Live) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(sr.Statuses) != 1 {
		t.Fatalf("want 1 status, got %d", len(sr.Statuses))
	}
	live := sr.Statuses[0].Live
	if len(live) < 1 {
		t.Fatal("live ring empty: /api/status did not backfill the sparkline")
	}
	if live[0].CPU != 52.6 || live[0].Mem != 75 || live[0].Load != 0.5 {
		t.Fatalf("live point = %+v, want cpu 52.6 mem 75 load 0.5", live[0])
	}
}
