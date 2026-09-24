package api

import (
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/sshconfig"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

type configCandidate struct {
	sshconfig.Entry
	Exists bool `json:"exists"`
}

func (s *Server) readSSHConfig(w http.ResponseWriter, _ *http.Request) {
	entries, err := sshconfig.Read(sshconfig.DefaultPath())
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}
	hosts := s.store.Hosts()
	out := make([]configCandidate, 0, len(entries))
	for _, e := range entries {
		out = append(out, configCandidate{Entry: e, Exists: hostNamed(hosts, e.Alias) != nil})
	}
	writeJSON(w, http.StatusOK, out)
}

// importSSHConfig creates hosts from ~/.ssh/config, importing each host's
// IdentityFile into the key store and wiring ProxyJump to jump hosts.
func (s *Server) importSSHConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Aliases []string `json:"aliases"`
		Group   string   `json:"group"`
	}
	if !decode(w, r, &in) {
		return
	}
	entries, err := sshconfig.Read(sshconfig.DefaultPath())
	if err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}

	var (
		imported []store.Host
		skipped  = []string{}
		failures = map[string]string{}
		byAlias  = map[string]sshconfig.Entry{}
	)
	for _, e := range entries {
		if !slices.Contains(in.Aliases, e.Alias) {
			continue
		}
		if hostNamed(s.store.Hosts(), e.Alias) != nil {
			skipped = append(skipped, e.Alias)
			continue
		}
		h := store.Host{
			Name: e.Alias, Address: e.HostName, Port: e.Port, User: e.User,
			Group: strings.TrimSpace(in.Group), Auth: store.AuthAgent, Monitor: true,
		}
		if e.IdentityFile != "" {
			if _, statErr := os.Stat(e.IdentityFile); statErr == nil {
				k, kerr := s.keys.Import(keys.ImportRequest{Path: e.IdentityFile})
				if kerr == nil || errors.Is(kerr, keys.ErrAlreadyImported) {
					h.Auth, h.KeyID = store.AuthKey, k.ID
				}
			}
		}
		saved, err := s.store.SaveHost(h)
		if err != nil {
			failures[e.Alias] = err.Error()
			continue
		}
		imported = append(imported, saved)
		byAlias[e.Alias] = e
	}

	// Second pass: jump hosts may be defined after the hosts that use them.
	for i, h := range imported {
		e := byAlias[h.Name]
		if e.ProxyJump == "" {
			continue
		}
		jump := hostNamed(s.store.Hosts(), sshconfig.FirstJumpAlias(e.ProxyJump))
		if jump == nil {
			failures[h.Name] = "jump host " + e.ProxyJump + " is not in Termward; set it manually"
			continue
		}
		h.JumpHostID = jump.ID
		if saved, err := s.store.SaveHost(h); err == nil {
			imported[i] = saved
		} else {
			failures[h.Name] = err.Error()
		}
	}

	s.monitor.Wake()
	s.hub.Publish("hosts_changed", nil)
	s.hub.Publish("keys_changed", nil)
	if imported == nil {
		imported = []store.Host{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped, "failed": failures})
}

func hostNamed(hosts []store.Host, name string) *store.Host {
	for i := range hosts {
		if strings.EqualFold(hosts[i].Name, name) {
			return &hosts[i]
		}
	}
	return nil
}
