package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

func (s *Server) listKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.store.Keys())
}

func (s *Server) scanKeys(w http.ResponseWriter, _ *http.Request) {
	c, err := s.keys.Scan()
	if err != nil {
		fail(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) generateKey(w http.ResponseWriter, r *http.Request) {
	var in keys.GenerateRequest
	if !decode(w, r, &in) {
		return
	}
	k, err := s.keys.Generate(in)
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	s.hub.Publish("keys_changed", nil)
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) importKey(w http.ResponseWriter, r *http.Request) {
	var in keys.ImportRequest
	if !decode(w, r, &in) {
		return
	}
	k, err := s.keys.Import(in)
	if errors.Is(err, keys.ErrAlreadyImported) {
		writeError(w, http.StatusConflict, "already_imported", "this key is already in Termward", k)
		return
	}
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	s.hub.Publish("keys_changed", nil)
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) renameKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid", "name is required", nil)
		return
	}
	k, err := s.store.RenameKey(r.PathValue("id"), in.Name)
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	s.hub.Publish("keys_changed", nil)
	writeJSON(w, http.StatusOK, k)
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	if err := s.keys.Delete(r.PathValue("id")); err != nil {
		fail(w, err, http.StatusConflict)
		return
	}
	s.hub.Publish("keys_changed", nil)
	w.WriteHeader(http.StatusNoContent)
}

// deployKey appends the public key to ~/.ssh/authorized_keys on a host (like
// ssh-copy-id), using whatever credentials currently work for that host.
// With switchAuth the host is then moved to this key — and moved back if the
// new key does not actually log in.
func (s *Server) deployKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HostID     string `json:"hostId"`
		SwitchAuth bool   `json:"switchAuth"`
	}
	if !decode(w, r, &in) {
		return
	}
	k, err := s.store.Key(r.PathValue("id"))
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	h, err := s.store.Host(in.HostID)
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	res, err := s.pool.Run(ctx, h.ID, "sh -s", strings.NewReader(deployScript(k.PublicKey)))
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	if res.ExitCode != 0 {
		writeError(w, http.StatusBadGateway, "deploy_failed",
			fmt.Sprintf("could not update authorized_keys (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr)), nil)
		return
	}
	already := strings.Contains(res.Stdout, "already")

	if in.SwitchAuth && !(h.Auth == store.AuthKey && h.KeyID == k.ID) {
		old := h
		h.Auth, h.KeyID = store.AuthKey, k.ID
		if h, err = s.store.SaveHost(h); err != nil {
			fail(w, err, http.StatusBadRequest)
			return
		}
		s.pool.Drop(h.ID)
		if _, err := s.pool.Client(ctx, h.ID); err != nil {
			if _, rerr := s.store.SaveHost(old); rerr == nil {
				s.pool.Drop(h.ID)
			}
			fail(w, fmt.Errorf("key was added but logging in with it failed, host settings were restored: %w", err), http.StatusBadGateway)
			return
		}
	}
	s.hub.Publish("hosts_changed", nil)
	writeJSON(w, http.StatusOK, map[string]any{"alreadyPresent": already, "host": h})
}

func deployScript(pubLine string) string {
	quoted := "'" + strings.ReplaceAll(pubLine, "'", `'\''`) + "'"
	return `KEY=` + quoted + `
umask 077
mkdir -p "$HOME/.ssh" || exit 1
f="$HOME/.ssh/authorized_keys"
touch "$f" || exit 1
if grep -qxF "$KEY" "$f"; then
	echo already
else
	# make sure we don't glue our key onto a last line without newline
	if [ -s "$f" ] && [ -n "$(tail -c1 "$f")" ]; then echo >> "$f"; fi
	printf '%s\n' "$KEY" >> "$f" || exit 1
	echo added
fi
chmod 700 "$HOME/.ssh"; chmod 600 "$f"
# SELinux (RHEL, AlmaLinux, Rocky, Fedora) rejects keys with a wrong label
command -v restorecon >/dev/null 2>&1 && restorecon -R "$HOME/.ssh" 2>/dev/null
exit 0
`
}
