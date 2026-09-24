package api

import (
	"context"
	"net/http"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

type hostInput struct {
	store.Host
	Password         string `json:"password"`
	RememberPassword bool   `json:"rememberPassword"`
}

func (s *Server) listHosts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Hosts())
}

func (s *Server) createHost(w http.ResponseWriter, r *http.Request) {
	var in hostInput
	if !decode(w, r, &in) {
		return
	}
	in.ID = ""
	s.saveHost(w, in, http.StatusCreated)
}

func (s *Server) updateHost(w http.ResponseWriter, r *http.Request) {
	var in hostInput
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("id")
	s.saveHost(w, in, http.StatusOK)
}

func (s *Server) saveHost(w http.ResponseWriter, in hostInput, status int) {
	h, err := s.store.SaveHost(in.Host)
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	if h.Auth == store.AuthPassword && in.Password != "" {
		_ = s.secrets.Put(secret.HostPassword(h.ID), in.Password, in.RememberPassword)
	}
	s.hub.Publish("hosts_changed", nil)
	// Settings may have changed: reconnect lazily with the new ones.
	s.pool.Drop(h.ID)
	if h.Monitor {
		go s.monitor.Poll(s.ctx, h.ID)
	} else {
		s.monitor.Wake() // prunes its status
	}
	writeJSON(w, status, h)
}

func (s *Server) deleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteHost(id); err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	s.pool.Drop(id)
	s.secrets.Forget(secret.HostPassword(id))
	s.monitor.Wake()
	s.hub.Publish("hosts_changed", nil)
	w.WriteHeader(http.StatusNoContent)
}

type connectInput struct {
	Password   string `json:"password"`
	Passphrase string `json:"passphrase"`
	Remember   bool   `json:"remember"`
}

// connectHost establishes (or reuses) the connection, storing any credential
// the user just typed. Errors tell the UI exactly what to ask for next.
func (s *Server) connectHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in connectInput
	if !decodeOptional(w, r, &in) {
		return
	}
	h, err := s.store.Host(id)
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	if in.Password != "" {
		_ = s.secrets.Put(secret.HostPassword(id), in.Password, in.Remember)
	}
	if in.Passphrase != "" && h.Auth == store.AuthKey {
		if err := s.keys.CheckPassphrase(h.KeyID, in.Passphrase); err != nil {
			fail(w, err, http.StatusBadRequest)
			return
		}
		_ = s.secrets.Put(secret.KeyPassphrase(h.KeyID), in.Passphrase, in.Remember)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := s.pool.Client(ctx, id); err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	if h.Monitor {
		go s.monitor.Poll(s.ctx, id)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"connected": true})
}

func (s *Server) disconnectHost(w http.ResponseWriter, r *http.Request) {
	s.pool.Drop(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) trustHost(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Fingerprint string `json:"fingerprint"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.pool.Known().Trust(r.PathValue("id"), in.Fingerprint); err != nil {
		fail(w, err, http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) checkHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.Host(id); err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	go s.monitor.Poll(s.ctx, id)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	connected := []string{}
	for _, h := range s.store.Hosts() {
		if s.pool.Connected(h.ID) {
			connected = append(connected, h.ID)
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Statuses  []health.Status `json:"statuses"`
		Connected []string        `json:"connected"`
	}{s.monitor.Snapshot(), connected})
}

func (s *Server) alerts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.monitor.Alerts())
}
