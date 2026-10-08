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
	"github.com/nguyenquocanhz/termward/core/internal/cloud"
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
	// HardwareInterval replaces the daily/weekly hardware check period
	// (development only).
	HardwareInterval time.Duration
	// MachineID is the platform machine id passed by the mobile apps
	// (ANDROID_ID, identifierForVendor) for the Termward Pro device
	// fingerprint; desktop builds read it from the OS.
	MachineID string
	// DeviceName names this device in the Termward Pro device list
	// (default: the host name).
	DeviceName string
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

	hub := api.NewHub()
	pro, err := cloud.New(cloud.Config{
		DataDir:    cfg.DataDir,
		Secrets:    sec,
		Version:    cfg.Version,
		MachineID:  cfg.MachineID,
		DeviceName: cfg.DeviceName,
		Host: func(id string) (string, bool) {
			h, err := st.Host(id)
			return h.Address, err == nil
		},
		OnChange: func(s cloud.Status) { hub.Publish("cloud", s) },
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(parent)
	// Every alert, health or hardware, passes through here exactly once (the
	// monitor publishes both): the UI, native notifications and Termward Pro
	// forwarding all hang off this one place. None of them may block.
	publish := func(kind string, payload any) {
		hub.Publish(kind, payload)
		a, ok := payload.(health.Alert)
		if !ok || kind != "alert" {
			return
		}
		pro.Enqueue(a)
		if cfg.OnAlert != nil {
			cfg.OnAlert(a)
		}
	}
	// The monitor runs the 30 s health poll and, because the pool can open
	// multiplexed sessions, also the per-host real-time metrics stream; both
	// publish through the same hub. Its lifecycle is bound to ctx via mon.Run.
	mon := health.NewMonitor(st, pool, publish)
	srv := api.New(ctx, api.Deps{
		Token: cfg.Token, Store: st, Keys: km, Secrets: sec, Pool: pool, Monitor: mon, Hub: hub, Cloud: pro,
		HardwareInterval: cfg.HardwareInterval,
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
	go srv.RunHardwareScheduler(ctx)
	go pro.Run(ctx)
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
