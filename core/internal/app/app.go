// Package app wires the core together. The desktop sidecar (cmd/termwardd)
// and the mobile library (package mobile) both start Termward through it, so
// every platform runs exactly the same code.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/api"
	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

type Config struct {
	DataDir string
	Addr    string // e.g. "127.0.0.1:0"
	Token   string
	Version string
	// Secrets overrides the OS keychain (mobile passes an encrypted file store).
	Secrets secret.Backend
	// ExtraKnownHosts are read-only known_hosts files, e.g. ~/.ssh/known_hosts.
	ExtraKnownHosts []string
	// OnAlert is called for every health alert, e.g. to post a native
	// notification on mobile while the UI is in the background.
	OnAlert func(health.Alert)
}

type Instance struct {
	Port int

	cancel context.CancelFunc
	srv    *http.Server
	pool   *sshx.Pool
	done   chan struct{}
}

func Start(parent context.Context, cfg Config) (*Instance, error) {
	if cfg.Token == "" {
		return nil, errors.New("token is required")
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	sec := secret.New()
	if cfg.Secrets != nil {
		sec = secret.NewWithBackend(cfg.Secrets)
	}
	km, err := keys.NewManager(filepath.Join(cfg.DataDir, "keys"), st, sec)
	if err != nil {
		return nil, err
	}
	known, err := sshx.NewKnownHosts(filepath.Join(cfg.DataDir, "known_hosts"), cfg.ExtraKnownHosts...)
	if err != nil {
		return nil, err
	}
	pool := sshx.NewPool(st, km, sec, known)

	ctx, cancel := context.WithCancel(parent)
	hub := api.NewHub()
	publish := hub.Publish
	if cfg.OnAlert != nil {
		publish = func(kind string, payload any) {
			hub.Publish(kind, payload)
			if a, ok := payload.(health.Alert); ok && kind == "alert" {
				cfg.OnAlert(a)
			}
		}
	}
	mon := health.NewMonitor(st, pool, publish)
	srv := api.New(ctx, api.Deps{
		Token: cfg.Token, Store: st, Keys: km, Secrets: sec, Pool: pool, Monitor: mon, Hub: hub,
	})
	srv.Version = cfg.Version

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		cancel()
		return nil, err
	}
	inst := &Instance{
		Port:   ln.Addr().(*net.TCPAddr).Port,
		cancel: cancel,
		srv:    &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second},
		pool:   pool,
		done:   make(chan struct{}),
	}
	go mon.Run(ctx)
	go func() {
		defer close(inst.done)
		if err := inst.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "termward: serve:", err)
		}
	}()
	return inst, nil
}

// Stop shuts the server down and closes every SSH connection.
func (i *Instance) Stop() {
	i.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = i.srv.Shutdown(ctx)
	i.pool.CloseAll()
	<-i.done
}

// Done is closed when the server stops serving.
func (i *Instance) Done() <-chan struct{} { return i.done }
