package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
)

// Origin checks are skipped on purpose: the socket is bound to 127.0.0.1 and
// every connection must present the session token.
var wsAccept = &websocket.AcceptOptions{InsecureSkipVerify: true}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, wsAccept)
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx := c.CloseRead(r.Context())
	ch, unsubscribe := s.hub.Subscribe()
	defer unsubscribe()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			if err := c.Ping(ctx); err != nil {
				return
			}
		}
	}
}

type termControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// terminal bridges an interactive PTY shell to xterm.js. Binary frames carry
// raw terminal bytes both ways; text frames carry JSON control messages.
func (s *Server) terminal(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hostID := q.Get("hostId")
	cols, _ := strconv.Atoi(q.Get("cols"))
	rows, _ := strconv.Atoi(q.Get("rows"))
	cols, rows = clampDim(cols, 80), clampDim(rows, 24)

	c, err := websocket.Accept(w, r, wsAccept)
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 20) // large pastes
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sendCtl := func(v any) {
		b, _ := json.Marshal(v)
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		_ = c.Write(wctx, websocket.MessageText, b)
		wcancel()
	}
	fatal := func(err error) {
		sendCtl(map[string]string{"type": "error", "message": err.Error()})
		c.Close(websocket.StatusNormalClosure, "")
	}

	dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
	sess, err := s.pool.NewSession(dctx, hostID)
	dcancel()
	if err != nil {
		fatal(err)
		return
	}
	defer sess.Close()

	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 115200, ssh.TTY_OP_OSPEED: 115200}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		fatal(err)
		return
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		fatal(err)
		return
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		fatal(err)
		return
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		fatal(err)
		return
	}
	if err := sess.Shell(); err != nil {
		fatal(err)
		return
	}
	sendCtl(map[string]string{"type": "ready"})

	pump := func(src io.Reader) {
		buf := make([]byte, 32<<10)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	var pumps sync.WaitGroup
	pumps.Go(func() { pump(stdout) })
	pumps.Go(func() { pump(stderr) })

	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := stdin.Write(data); err != nil {
					return
				}
				continue
			}
			var ctl termControl
			if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" {
				_ = sess.WindowChange(clampDim(ctl.Rows, 24), clampDim(ctl.Cols, 80))
			}
		}
	}()

	waitErr := make(chan error, 1)
	go func() { waitErr <- sess.Wait() }()
	select {
	case err := <-waitErr:
		code := 0
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitStatus()
		}
		// Let the last output reach the UI before announcing the exit.
		flushed := make(chan struct{})
		go func() { pumps.Wait(); close(flushed) }()
		select {
		case <-flushed:
		case <-time.After(time.Second):
		}
		sendCtl(map[string]any{"type": "exit", "code": code})
		c.Close(websocket.StatusNormalClosure, "")
	case <-ctx.Done():
		// UI closed the tab: hang up the remote shell.
		_ = sess.Signal(ssh.SIGHUP)
	}
}

func clampDim(v, def int) int {
	if v <= 0 {
		return def
	}
	return min(v, 1000)
}
