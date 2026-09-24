// Package sshconfig reads ~/.ssh/config so existing hosts can be imported in
// one step instead of being retyped.
package sshconfig

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kevinburke/ssh_config"
)

type Entry struct {
	Alias        string `json:"alias"`
	HostName     string `json:"hostName"`
	User         string `json:"user"`
	Port         int    `json:"port"`
	IdentityFile string `json:"identityFile,omitempty"`
	ProxyJump    string `json:"proxyJump,omitempty"`
}

func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "config")
}

// Read lists concrete Host aliases (wildcard patterns only supply defaults).
func Read(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, err
	}

	defaultUser := ""
	if u, err := user.Current(); err == nil {
		defaultUser = u.Username
		if i := strings.LastIndexAny(defaultUser, `\`); i >= 0 { // DOMAIN\user on Windows
			defaultUser = defaultUser[i+1:]
		}
	}

	var out []Entry
	seen := map[string]bool{}
	for _, h := range cfg.Hosts {
		for _, p := range h.Patterns {
			alias := p.String()
			if alias == "" || strings.ContainsAny(alias, "*?!") || seen[alias] {
				continue
			}
			seen[alias] = true
			get := func(key string) string {
				v, _ := cfg.Get(alias, key)
				return strings.TrimSpace(v)
			}
			e := Entry{Alias: alias, HostName: get("HostName"), User: get("User"), ProxyJump: get("ProxyJump")}
			if e.HostName == "" {
				e.HostName = alias
			}
			e.HostName = strings.ReplaceAll(e.HostName, "%h", alias)
			if e.User == "" {
				e.User = defaultUser
			}
			e.Port, _ = strconv.Atoi(get("Port"))
			if e.Port == 0 {
				e.Port = 22
			}
			if ids, _ := cfg.GetAll(alias, "IdentityFile"); len(ids) > 0 {
				e.IdentityFile = ExpandPath(ids[0])
			}
			if strings.EqualFold(e.ProxyJump, "none") {
				e.ProxyJump = ""
			}
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b Entry) int { return strings.Compare(a.Alias, b.Alias) })
	if out == nil {
		out = []Entry{}
	}
	return out, nil
}

// ExpandPath resolves ~ and %d (home directory) as OpenSSH does.
func ExpandPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	p = strings.ReplaceAll(p, "%d", home)
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(home, p[1:])
	}
	return filepath.Clean(p)
}

// FirstJumpAlias returns the first hop of a ProxyJump value as a bare alias
// ("user@bastion:2222,next" -> "bastion").
func FirstJumpAlias(proxyJump string) string {
	hop, _, _ := strings.Cut(proxyJump, ",")
	if i := strings.LastIndex(hop, "@"); i >= 0 {
		hop = hop[i+1:]
	}
	if h, _, ok := strings.Cut(hop, ":"); ok && !strings.Contains(h, "[") {
		hop = h
	}
	return strings.TrimSpace(hop)
}
