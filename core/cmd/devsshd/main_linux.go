// Command devsshd is a throwaway SSH server for developing Termward against a
// real Linux shell (for example inside WSL) without installing or touching
// OpenSSH. It only accepts keys listed in -authorized-keys and runs everything
// as the user who started it. Never expose it to a network.
//
//	go build -o devsshd ./cmd/devsshd   (GOOS=linux)
//	./devsshd -authorized-keys ~/.ssh/tw_dev.pub -addr 127.0.0.1:2222
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:2222", "listen address")
	authPath := flag.String("authorized-keys", "", "authorized_keys file (required)")
	hostKeyPath := flag.String("host-key", "devsshd_host_ed25519", "host key file (created if missing)")
	flag.Parse()

	allowed, err := loadAuthorized(*authPath)
	if err != nil {
		log.Fatalf("authorized keys: %v", err)
	}
	hostKey, err := loadOrCreateHostKey(*hostKeyPath)
	if err != nil {
		log.Fatalf("host key: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			for _, a := range allowed {
				if bytes.Equal(a.Marshal(), k.Marshal()) {
					return nil, nil
				}
			}
			return nil, errors.New("key not authorized")
		},
	}
	cfg.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("devsshd on %s, host key %s", ln.Addr(), ssh.FingerprintSHA256(hostKey.PublicKey()))
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go serve(c, cfg)
	}
}

func loadAuthorized(path string) ([]ssh.PublicKey, error) {
	if path == "" {
		return nil, errors.New("-authorized-keys is required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var keys []ssh.PublicKey
	for len(bytes.TrimSpace(b)) > 0 {
		k, _, _, rest, err := ssh.ParseAuthorizedKey(b)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
		b = rest
	}
	return keys, nil
}

func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	if b, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(b)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "devsshd")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

func serve(c net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "only sessions are supported")
			continue
		}
		ch, reqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go session(ch, reqs)
	}
}

type ptyReq struct {
	Term          string
	Cols, Rows    uint32
	Width, Height uint32
	Modes         string
}

func session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	var (
		mu   sync.Mutex
		term = ""
		size = &pty.Winsize{Cols: 80, Rows: 24}
		ptmx *os.File
	)
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var p ptyReq
			if ssh.Unmarshal(req.Payload, &p) == nil {
				term, size = p.Term, &pty.Winsize{Cols: uint16(p.Cols), Rows: uint16(p.Rows)}
			}
			req.Reply(true, nil)
		case "window-change":
			var w struct{ Cols, Rows, Width, Height uint32 }
			if ssh.Unmarshal(req.Payload, &w) == nil {
				mu.Lock()
				size = &pty.Winsize{Cols: uint16(w.Cols), Rows: uint16(w.Rows)}
				if ptmx != nil {
					_ = pty.Setsize(ptmx, size)
				}
				mu.Unlock()
			}
		case "env", "signal":
			req.Reply(true, nil)
		case "shell", "exec":
			var cmd *exec.Cmd
			if req.Type == "exec" {
				var p struct{ Cmd string }
				ssh.Unmarshal(req.Payload, &p)
				cmd = exec.Command("/bin/sh", "-c", p.Cmd)
			} else {
				shell := os.Getenv("SHELL")
				if shell == "" {
					shell = "/bin/bash"
				}
				cmd = exec.Command(shell, "-l")
			}
			cmd.Dir, _ = os.UserHomeDir()
			cmd.Env = os.Environ()
			if term != "" {
				cmd.Env = append(cmd.Env, "TERM="+term)
			}
			req.Reply(true, nil)

			code := 0
			if term != "" {
				f, err := pty.StartWithSize(cmd, size)
				if err != nil {
					code = 127
				} else {
					mu.Lock()
					ptmx = f
					mu.Unlock()
					go io.Copy(f, ch)
					go io.Copy(ch, f)
					code = exitCode(cmd.Wait())
					f.Close()
				}
			} else {
				cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
				code = exitCode(cmd.Run())
			}
			status := make([]byte, 4)
			binary.BigEndian.PutUint32(status, uint32(code))
			ch.SendRequest("exit-status", false, status)
			return
		default:
			req.Reply(false, nil)
		}
	}
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	if err != nil {
		return 127
	}
	return 0
}
