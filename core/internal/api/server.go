// Package api exposes the core to the desktop UI over HTTP + WebSocket on
// 127.0.0.1. Every request must carry the random token the desktop app
// generated when it started the core.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/sshx"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

type Server struct {
	Version string

	token   string
	store   *store.Store
	keys    *keys.Manager
	secrets *secret.Store
	pool    *sshx.Pool
	monitor *health.Monitor
	hub     *Hub

	ctx  context.Context // cancelled on shutdown; parent of background jobs
	jobs sync.Map        // exec job id -> context.CancelFunc
}

type Deps struct {
	Token   string
	Store   *store.Store
	Keys    *keys.Manager
	Secrets *secret.Store
	Pool    *sshx.Pool
	Monitor *health.Monitor
	Hub     *Hub
}

func New(ctx context.Context, d Deps) *Server {
	return &Server{
		token: d.Token, store: d.Store, keys: d.Keys, secrets: d.Secrets,
		pool: d.Pool, monitor: d.Monitor, hub: d.Hub, ctx: ctx,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", s.info)

	mux.HandleFunc("GET /api/hosts", s.listHosts)
	mux.HandleFunc("POST /api/hosts", s.createHost)
	mux.HandleFunc("PUT /api/hosts/{id}", s.updateHost)
	mux.HandleFunc("DELETE /api/hosts/{id}", s.deleteHost)
	mux.HandleFunc("POST /api/hosts/{id}/connect", s.connectHost)
	mux.HandleFunc("POST /api/hosts/{id}/disconnect", s.disconnectHost)
	mux.HandleFunc("POST /api/hosts/{id}/trust", s.trustHost)
	mux.HandleFunc("POST /api/hosts/{id}/check", s.checkHost)
	mux.HandleFunc("POST /api/hosts/{id}/power", s.powerHost)

	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/alerts", s.alerts)

	mux.HandleFunc("GET /api/keys", s.listKeys)
	mux.HandleFunc("GET /api/keys/scan", s.scanKeys)
	mux.HandleFunc("POST /api/keys/generate", s.generateKey)
	mux.HandleFunc("POST /api/keys/import", s.importKey)
	mux.HandleFunc("PATCH /api/keys/{id}", s.renameKey)
	mux.HandleFunc("DELETE /api/keys/{id}", s.deleteKey)
	mux.HandleFunc("POST /api/keys/{id}/deploy", s.deployKey)

	mux.HandleFunc("GET /api/snippets", s.listSnippets)
	mux.HandleFunc("POST /api/snippets", s.saveSnippet)
	mux.HandleFunc("PUT /api/snippets/{id}", s.saveSnippet)
	mux.HandleFunc("DELETE /api/snippets/{id}", s.deleteSnippet)

	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	mux.HandleFunc("POST /api/exec", s.startExec)
	mux.HandleFunc("POST /api/exec/{id}/cancel", s.cancelExec)

	mux.HandleFunc("GET /api/import/ssh-config", s.readSSHConfig)
	mux.HandleFunc("POST /api/import/ssh-config", s.importSSHConfig)

	mux.HandleFunc("GET /ws/events", s.events)
	mux.HandleFunc("GET /ws/terminal", s.terminal)

	return s.middleware(mux)
}

// middleware allows cross-origin calls (the UI is loaded from file:// or the
// Vite dev server) and rejects anything without the session token. CORS is
// safe here because authentication is a bearer token, never a cookie.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		h.Set("Cache-Control", "no-store")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid token", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" && strings.HasPrefix(r.URL.Path, "/ws/") {
		got = r.URL.Query().Get("token") // browsers cannot set headers on WebSockets
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"name": "termward", "version": s.Version})
}

// ------------------------------------------------------------ helpers

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string, details any) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg, Details: details}})
}

// fail maps domain errors to HTTP responses; fallback is used for anything
// unrecognized (400 for bad input, 502 for remote failures).
func fail(w http.ResponseWriter, err error, fallback int) {
	var (
		unknown  *sshx.UnknownHostError
		changed  *sshx.HostKeyChangedError
		authReq  *sshx.AuthRequiredError
		needPass *keys.NeedPassphraseError
	)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.As(err, &unknown):
		writeError(w, http.StatusConflict, "unknown_host", err.Error(), unknown)
	case errors.As(err, &changed):
		writeError(w, http.StatusConflict, "host_key_changed", err.Error(), changed)
	case errors.As(err, &authReq):
		writeError(w, http.StatusUnauthorized, "auth_required", err.Error(), authReq)
	case errors.As(err, &needPass):
		writeError(w, http.StatusUnauthorized, "auth_required", err.Error(),
			&sshx.AuthRequiredError{Kind: "passphrase", KeyID: needPass.KeyID, Wrong: needPass.Wrong})
	case errors.Is(err, sshx.ErrAuthRejected):
		writeError(w, http.StatusBadGateway, "auth_failed", err.Error(), nil)
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", "the server did not answer in time", nil)
	default:
		code := "invalid"
		if fallback >= 500 {
			code = "connect_failed"
		}
		writeError(w, fallback, code, err.Error(), nil)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBody(w, r, v, false)
}

// decodeOptional accepts an empty body.
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBody(w, r, v, true)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any, optional bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	err := json.NewDecoder(r.Body).Decode(v)
	if optional && errors.Is(err, io.EOF) {
		return true
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "invalid JSON body: "+err.Error(), nil)
		return false
	}
	return true
}
