// Package sshx owns every outgoing SSH connection. One multiplexed client per
// host is kept alive and shared by terminals, health checks and commands, so
// a host is only dialed (and authenticated) once.
package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

const (
	dialTimeout       = 15 * time.Second
	keepaliveInterval = 20 * time.Second
	maxJumpDepth      = 5
	maxOutput         = 1 << 20
)

// AuthRequiredError asks the UI to collect a password or key passphrase.
type AuthRequiredError struct {
	HostID string `json:"hostId"`
	Kind   string `json:"kind"` // "password" | "passphrase"
	KeyID  string `json:"keyId,omitempty"`
	Wrong  bool   `json:"wrong"`
}

func (e *AuthRequiredError) Error() string {
	if e.Wrong {
		return "wrong " + e.Kind
	}
	return e.Kind + " required"
}

type Pool struct {
	store   *store.Store
	keys    *keys.Manager
	secrets *secret.Store
	known   *KnownHosts

	mu      sync.Mutex
	clients map[string]*ssh.Client
	dialing map[string]*dialCall
}

type dialCall struct {
	done   chan struct{}
	client *ssh.Client
	err    error
}

func NewPool(st *store.Store, km *keys.Manager, sec *secret.Store, known *KnownHosts) *Pool {
	return &Pool{
		store: st, keys: km, secrets: sec, known: known,
		clients: map[string]*ssh.Client{},
		dialing: map[string]*dialCall{},
	}
}

func (p *Pool) Known() *KnownHosts { return p.known }

// Client returns a live connection to the host, dialing it if needed.
// Concurrent callers for the same host share a single dial.
func (p *Pool) Client(ctx context.Context, hostID string) (*ssh.Client, error) {
	return p.client(ctx, hostID, 0)
}

func (p *Pool) client(ctx context.Context, hostID string, depth int) (*ssh.Client, error) {
	if depth > maxJumpDepth {
		return nil, errors.New("jump host chain is too deep")
	}
	p.mu.Lock()
	if c, ok := p.clients[hostID]; ok {
		p.mu.Unlock()
		return c, nil
	}
	if call, ok := p.dialing[hostID]; ok {
		p.mu.Unlock()
		select {
		case <-call.done:
			return call.client, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &dialCall{done: make(chan struct{})}
	p.dialing[hostID] = call
	p.mu.Unlock()

	call.client, call.err = p.dial(ctx, hostID, depth)

	p.mu.Lock()
	delete(p.dialing, hostID)
	if call.err == nil {
		p.clients[hostID] = call.client
	}
	p.mu.Unlock()
	close(call.done)

	if call.err == nil {
		go p.watch(hostID, call.client)
	}
	return call.client, call.err
}

// watch removes the client from the pool when it dies and sends keepalives so
// silently dropped connections are noticed within ~40 seconds.
func (p *Pool) watch(hostID string, c *ssh.Client) {
	closed := make(chan struct{})
	go func() {
		_ = c.Wait()
		close(closed)
	}()
	t := time.NewTicker(keepaliveInterval)
	defer t.Stop()
	for {
		select {
		case <-closed:
			p.forget(hostID, c)
			return
		case <-t.C:
			if err := keepalive(c); err != nil {
				c.Close()
			}
		}
	}
}

func keepalive(c *ssh.Client) error {
	res := make(chan error, 1)
	go func() {
		_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
		res <- err
	}()
	select {
	case err := <-res:
		return err
	case <-time.After(keepaliveInterval):
		return errors.New("keepalive timeout")
	}
}

func (p *Pool) forget(hostID string, c *ssh.Client) {
	p.mu.Lock()
	if p.clients[hostID] == c {
		delete(p.clients, hostID)
	}
	p.mu.Unlock()
}

// Drop closes the host's connection, e.g. after its settings changed.
func (p *Pool) Drop(hostID string) {
	p.mu.Lock()
	c := p.clients[hostID]
	delete(p.clients, hostID)
	p.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

func (p *Pool) Connected(hostID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.clients[hostID]
	return ok
}

func (p *Pool) CloseAll() {
	p.mu.Lock()
	cs := p.clients
	p.clients = map[string]*ssh.Client{}
	p.mu.Unlock()
	for _, c := range cs {
		c.Close()
	}
}

func (p *Pool) dial(ctx context.Context, hostID string, depth int) (*ssh.Client, error) {
	h, err := p.store.Host(hostID)
	if err != nil {
		return nil, err
	}
	auth, cleanup, err := p.authMethods(ctx, h)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	addr := net.JoinHostPort(h.Address, strconv.Itoa(h.Port))
	cfg := &ssh.ClientConfig{
		User:              h.User,
		Auth:              auth,
		HostKeyCallback:   p.known.Callback(h.ID),
		HostKeyAlgorithms: p.known.HostKeyAlgorithms(addr),
		Timeout:           dialTimeout,
	}

	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	var conn net.Conn
	if h.JumpHostID != "" {
		jump, err := p.client(ctx, h.JumpHostID, depth+1)
		if err != nil {
			return nil, fmt.Errorf("jump host: %w", err)
		}
		conn, err = jump.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("via jump host: %w", err)
		}
	} else {
		d := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
		conn, err = d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
	}

	// Abort the handshake if the context ends (the server may never answer).
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if !stop() && err == nil {
		c.Close()
		return nil, ctx.Err()
	}
	if err != nil {
		conn.Close()
		return nil, p.classifyDialError(h, err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func (p *Pool) classifyDialError(h store.Host, err error) error {
	var unknown *UnknownHostError
	var changed *HostKeyChangedError
	if errors.As(err, &unknown) || errors.As(err, &changed) {
		return err
	}
	if strings.Contains(err.Error(), "unable to authenticate") {
		switch h.Auth {
		case store.AuthPassword:
			p.secrets.Forget(secret.HostPassword(h.ID))
			return &AuthRequiredError{HostID: h.ID, Kind: "password", Wrong: true}
		case store.AuthKey:
			return fmt.Errorf("%w: deploy the public key to this host first", ErrAuthRejected)
		case store.AuthAgent:
			return fmt.Errorf("%w: no key offered by ssh-agent was accepted", ErrAuthRejected)
		}
	}
	return err
}

var (
	// ErrAuthRejected: the server is reachable but refused our credentials.
	ErrAuthRejected = errors.New("server rejected authentication")
	// ErrConfig: the host cannot be dialed until its settings are fixed.
	ErrConfig = errors.New("host configuration")
)

func (p *Pool) authMethods(ctx context.Context, h store.Host) ([]ssh.AuthMethod, func(), error) {
	noop := func() {}
	switch h.Auth {
	case store.AuthKey:
		k, err := p.store.Key(h.KeyID)
		if err != nil {
			return nil, noop, fmt.Errorf("%w: the key assigned to this host no longer exists", ErrConfig)
		}
		signer, err := p.keys.Signer(k)
		var need *keys.NeedPassphraseError
		if errors.As(err, &need) {
			return nil, noop, &AuthRequiredError{HostID: h.ID, Kind: "passphrase", KeyID: k.ID, Wrong: need.Wrong}
		}
		if err != nil {
			return nil, noop, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, noop, nil

	case store.AuthPassword:
		pw, err := p.secrets.Get(secret.HostPassword(h.ID))
		if err != nil {
			return nil, noop, &AuthRequiredError{HostID: h.ID, Kind: "password"}
		}
		answer := func(_, _ string, questions []string, echos []bool) ([]string, error) {
			out := make([]string, len(questions))
			for i := range questions {
				if !echos[i] {
					out[i] = pw
				}
			}
			return out, nil
		}
		return []ssh.AuthMethod{ssh.Password(pw), ssh.KeyboardInteractive(answer)}, noop, nil

	case store.AuthAgent:
		ag, closeAgent, err := dialAgent(ctx)
		if err != nil {
			return nil, noop, fmt.Errorf("%w: ssh-agent is not available (%v)", ErrConfig, err)
		}
		return []ssh.AuthMethod{ssh.PublicKeysCallback(ag.Signers)}, closeAgent, nil
	}
	return nil, noop, fmt.Errorf("unknown auth method %q", h.Auth)
}

// ------------------------------------------------------------ commands

type Result struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	Truncated  bool   `json:"truncated"`
}

// NewSession opens a channel on the pooled client. A stale connection is
// dropped and redialed once.
func (p *Pool) NewSession(ctx context.Context, hostID string) (*ssh.Session, error) {
	for attempt := 0; ; attempt++ {
		c, err := p.Client(ctx, hostID)
		if err != nil {
			return nil, err
		}
		s, err := newSession(ctx, c)
		if err == nil {
			return s, nil
		}
		p.forget(hostID, c)
		c.Close()
		if attempt == 1 || ctx.Err() != nil {
			return nil, err
		}
	}
}

func newSession(ctx context.Context, c *ssh.Client) (*ssh.Session, error) {
	type res struct {
		s   *ssh.Session
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := c.NewSession()
		ch <- res{s, err}
	}()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.s != nil {
				r.s.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// Run executes a non-interactive command and collects its output. stdin may
// be nil.
func (p *Pool) Run(ctx context.Context, hostID, cmd string, stdin io.Reader) (Result, error) {
	start := time.Now()
	s, err := p.NewSession(ctx, hostID)
	if err != nil {
		return Result{}, err
	}
	defer s.Close()

	var stdout, stderr limitedBuffer
	s.Stdout, s.Stderr, s.Stdin = &stdout, &stderr, stdin
	done := make(chan error, 1)
	go func() { done <- s.Run(cmd) }()

	select {
	case err = <-done:
	case <-ctx.Done():
		_ = s.Signal(ssh.SIGKILL)
		return Result{}, ctx.Err()
	}

	r := Result{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: time.Since(start).Milliseconds(),
		Truncated:  stdout.truncated || stderr.truncated,
	}
	var exitErr *ssh.ExitError
	var missing *ssh.ExitMissingError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		r.ExitCode = exitErr.ExitStatus()
	case errors.As(err, &missing):
		r.ExitCode = -1
	default:
		return r, err
	}
	return r, nil
}

type limitedBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := maxOutput - b.Len(); room < len(p) {
		b.truncated = true
		if room <= 0 {
			return n, nil
		}
		p = p[:room]
	}
	b.Buffer.Write(p)
	return n, nil
}
