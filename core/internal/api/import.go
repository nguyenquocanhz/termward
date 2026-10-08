package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nguyenquocanhz/termward/core/internal/keys"
	"github.com/nguyenquocanhz/termward/core/internal/sshconfig"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// Files larger than this are not an SSH config or a known_hosts.
const maxImportFile = 4 << 20

// importSource says which file an import looked at and what state it is in, so
// the UI can tell "there is no such file" from "the file lists no servers".
// It never carries the contents of the file: Error is one of the codes below.
type importSource struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	// "" (read fine), "parse" (not the expected format; ErrorLine when known),
	// "not_a_file", "too_large" or "unreadable".
	Error     string `json:"error,omitempty"`
	ErrorLine int    `json:"errorLine,omitempty"`
}

type configCandidate struct {
	sshconfig.Entry
	Exists bool `json:"exists"`
}

type knownCandidate struct {
	sshconfig.KnownHost
	Exists bool `json:"exists"`
}

// importFile resolves the file to read: the one the user picked, else def.
// ok is false when the request was answered with an error.
func importFile(w http.ResponseWriter, picked, def string) (src importSource, ok bool) {
	src.Path = def
	if picked = strings.TrimSpace(picked); picked != "" {
		if !filepath.IsAbs(picked) {
			writeError(w, http.StatusBadRequest, "invalid", "path must be absolute", nil)
			return src, false
		}
		src.Path = filepath.Clean(picked)
	}
	fi, err := os.Stat(src.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		src.Exists, src.Error = true, "unreadable"
	case !fi.Mode().IsRegular():
		src.Exists, src.Error = true, "not_a_file"
	case fi.Size() > maxImportFile:
		src.Exists, src.Error = true, "too_large"
	default:
		src.Exists = true
	}
	return src, true
}

// readConfig parses src as an SSH config. Problems land in src.Error.
func readConfig(src *importSource) []sshconfig.Entry {
	if !src.Exists || src.Error != "" {
		return []sshconfig.Entry{}
	}
	entries, err := sshconfig.Read(src.Path)
	var pe *sshconfig.ParseError
	switch {
	case errors.As(err, &pe):
		src.Error, src.ErrorLine = "parse", pe.Line
	case err != nil:
		src.Error = "unreadable"
	}
	if entries == nil {
		entries = []sshconfig.Entry{}
	}
	return entries
}

// pickedPath reads the file the user chose from the body of a "read" request
// (a body rather than the URL, so local paths stay out of request lines).
func pickedPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Method != http.MethodPost {
		return "", true
	}
	var in struct {
		Path string `json:"path"`
	}
	if !decodeOptional(w, r, &in) {
		return "", false
	}
	return in.Path, true
}

func (s *Server) readSSHConfig(w http.ResponseWriter, r *http.Request) {
	picked, ok := pickedPath(w, r)
	if !ok {
		return
	}
	src, ok := importFile(w, picked, sshconfig.DefaultPath())
	if !ok {
		return
	}
	entries := readConfig(&src)
	hosts := s.store.Hosts()
	out := make([]configCandidate, 0, len(entries))
	for _, e := range entries {
		out = append(out, configCandidate{Entry: e, Exists: hostNamed(hosts, e.Alias) != nil})
	}
	writeJSON(w, http.StatusOK, struct {
		importSource
		Entries []configCandidate `json:"entries"`
	}{src, out})
}

// importSSHConfig creates hosts from an SSH config (~/.ssh/config unless the
// user picked another file), importing each IdentityFile into the key store
// and wiring ProxyJump to jump hosts.
func (s *Server) importSSHConfig(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Aliases []string `json:"aliases"`
		Group   string   `json:"group"`
		Path    string   `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	src, ok := importFile(w, in.Path, sshconfig.DefaultPath())
	if !ok {
		return
	}
	entries := readConfig(&src)
	if src.Error != "" {
		writeError(w, http.StatusBadRequest, "invalid", "the SSH config could not be read", src)
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

// readKnownHosts lists the servers the user has already connected to with
// OpenSSH. known_hosts has no user names: DefaultUser is only a suggestion.
func (s *Server) readKnownHosts(w http.ResponseWriter, r *http.Request) {
	picked, ok := pickedPath(w, r)
	if !ok {
		return
	}
	src, ok := importFile(w, picked, sshconfig.DefaultKnownHostsPath())
	if !ok {
		return
	}
	var kh sshconfig.KnownHosts
	if src.Exists && src.Error == "" {
		var err error
		if kh, err = sshconfig.ReadKnownHosts(src.Path); err != nil {
			src.Error = "unreadable"
		} else if len(kh.Hosts) == 0 && kh.Hashed == 0 && kh.Invalid > 0 {
			src.Error = "parse" // nothing in it looks like known_hosts
		}
	}
	hosts := s.store.Hosts()
	out := make([]knownCandidate, 0, len(kh.Hosts))
	for _, k := range kh.Hosts {
		out = append(out, knownCandidate{KnownHost: k, Exists: hostAt(hosts, k.Address, k.Port) != nil})
	}
	writeJSON(w, http.StatusOK, struct {
		importSource
		Entries     []knownCandidate `json:"entries"`
		Hashed      int              `json:"hashed"`
		Invalid     int              `json:"invalid"`
		DefaultUser string           `json:"defaultUser"`
	}{src, out, kh.Hashed, kh.Invalid, sshconfig.DefaultUser()})
}

// importKnownHosts adds the servers picked from known_hosts. The user was
// guessed, so they start unmonitored: Termward must not begin signing in to
// machines in the background with a name nobody confirmed works.
func (s *Server) importKnownHosts(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hosts []struct {
			Name    string `json:"name"`
			Address string `json:"address"`
			Port    int    `json:"port"`
			User    string `json:"user"`
			Group   string `json:"group"`
		} `json:"hosts"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Hosts) == 0 {
		writeError(w, http.StatusBadRequest, "invalid", "select at least one server", nil)
		return
	}
	var (
		imported = []store.Host{}
		skipped  = []string{}
		failures = map[string]string{}
	)
	for _, c := range in.Hosts {
		label := strings.TrimSpace(c.Name)
		if label == "" {
			label = strings.TrimSpace(c.Address)
		}
		port := c.Port
		if port == 0 {
			port = 22
		}
		if hostAt(s.store.Hosts(), c.Address, port) != nil {
			skipped = append(skipped, label)
			continue
		}
		saved, err := s.store.SaveHost(store.Host{
			Name: c.Name, Address: c.Address, Port: port, User: c.User, Group: c.Group,
			Auth: store.AuthAgent, Monitor: false,
		})
		if err != nil {
			failures[label] = err.Error()
			continue
		}
		imported = append(imported, saved)
	}
	if len(imported) > 0 {
		s.monitor.Wake()
		s.hw.Wake()
		s.hub.Publish("hosts_changed", nil)
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

// hostAt finds a host by where it connects to.
func hostAt(hosts []store.Host, address string, port int) *store.Host {
	address = strings.TrimSpace(address)
	for i := range hosts {
		if strings.EqualFold(hosts[i].Address, address) && hosts[i].Port == port {
			return &hosts[i]
		}
	}
	return nil
}
