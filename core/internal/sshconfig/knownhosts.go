package sshconfig

import (
	"bufio"
	"encoding/base64"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// KnownHost is one server found in a known_hosts file. The file records where
// the user has connected, not as whom, so there is no user here.
type KnownHost struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// KnownHosts is what could be read from a known_hosts file. Hashed counts the
// lines written with HashKnownHosts (their host names cannot be recovered) and
// Invalid the lines that are not known_hosts entries at all: either way the
// list of hosts is not the whole file.
type KnownHosts struct {
	Hosts   []KnownHost
	Hashed  int
	Invalid int
}

func DefaultKnownHostsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "known_hosts")
}

// DefaultUser is the local account name, which OpenSSH uses when nothing else
// names a user.
func DefaultUser() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	name := u.Username
	if i := strings.LastIndexAny(name, `\`); i >= 0 { // DOMAIN\user on Windows
		name = name[i+1:]
	}
	return name
}

// ReadKnownHosts parses the file at path. A missing file is an error
// (os.ErrNotExist): the caller tells that apart from an empty one.
func ReadKnownHosts(path string) (KnownHosts, error) {
	f, err := os.Open(path)
	if err != nil {
		return KnownHosts{}, err
	}
	defer f.Close()
	return ParseKnownHosts(f)
}

type knownNode struct {
	host   string
	port   int
	ip     bool
	parent int
	label  string // a name listed on the same line (IP nodes only)
}

// ParseKnownHosts lists the servers in a known_hosts file. Names and addresses
// that share a host key are one server: the row keeps the IP as the address
// and the name as its label. Several IPs behind one key (cloned machines) stay
// separate rows.
func ParseKnownHosts(r io.Reader) (KnownHosts, error) {
	var (
		out   KnownHosts
		nodes []knownNode
		byID  = map[string]int{}
		byKey = map[string]int{}
	)
	var find func(int) int
	find = func(i int) int {
		if nodes[i].parent != i {
			nodes[i].parent = find(nodes[i].parent)
		}
		return nodes[i].parent
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if ra > rb { // the earliest node stays the root, keeping file order
			ra, rb = rb, ra
		}
		nodes[rb].parent = ra
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if strings.HasPrefix(fields[0], "@") {
			// @revoked keys and @cert-authority lines are not servers.
			if fields[0] != "@revoked" && fields[0] != "@cert-authority" {
				out.Invalid++
			}
			continue
		}
		if len(fields) < 3 || !plausibleKey(fields[1], fields[2]) {
			out.Invalid++
			continue
		}
		if strings.HasPrefix(fields[0], "|") {
			out.Hashed++
			continue
		}

		var onLine []int
		for _, pat := range strings.Split(fields[0], ",") {
			host, port, ok := splitKnownHost(pat)
			if !ok {
				continue
			}
			id := strings.ToLower(host) + " " + strconv.Itoa(port)
			i, seen := byID[id]
			if !seen {
				i = len(nodes)
				nodes = append(nodes, knownNode{host: host, port: port, ip: isIP(host), parent: i})
				byID[id] = i
			}
			onLine = append(onLine, i)
		}
		if len(onLine) == 0 {
			continue // only wildcard or negated patterns
		}
		key := fields[1] + " " + fields[2]
		if first, ok := byKey[key]; ok {
			union(first, onLine[0])
		} else {
			byKey[key] = onLine[0]
		}
		firstName := ""
		for _, i := range onLine {
			union(onLine[0], i)
			if !nodes[i].ip && firstName == "" {
				firstName = nodes[i].host
			}
		}
		for _, i := range onLine {
			if nodes[i].ip && nodes[i].label == "" {
				nodes[i].label = firstName
			}
		}
	}
	if err := sc.Err(); err != nil {
		return KnownHosts{}, err
	}

	// Group the nodes by server, in the order the file first mentions each.
	var roots []int
	members := map[int][]int{}
	for i := range nodes {
		root := find(i)
		if _, ok := members[root]; !ok {
			roots = append(roots, root)
		}
		members[root] = append(members[root], i)
	}
	for _, root := range roots {
		var ips, names []int
		used := map[string]bool{}
		for _, i := range members[root] {
			if nodes[i].ip {
				ips = append(ips, i)
				used[strings.ToLower(nodes[i].label)] = true
			} else {
				names = append(names, i)
			}
		}
		if len(ips) == 0 {
			n := nodes[names[0]]
			out.Hosts = append(out.Hosts, KnownHost{Name: n.host, Address: n.host, Port: n.port})
			continue
		}
		// Names recorded on their own line label the addresses still without one.
		var spare []string
		for _, i := range names {
			if !used[strings.ToLower(nodes[i].host)] {
				spare = append(spare, nodes[i].host)
			}
		}
		for _, i := range ips {
			n := nodes[i]
			if n.label == "" && len(spare) > 0 {
				n.label, spare = spare[0], spare[1:]
			}
			if n.label == "" {
				n.label = n.host
			}
			out.Hosts = append(out.Hosts, KnownHost{Name: n.label, Address: n.host, Port: n.port})
		}
	}

	// One name on several ports (or several cloned machines) would give rows
	// nobody can tell apart: spell those out.
	count := map[string]int{}
	for _, h := range out.Hosts {
		count[strings.ToLower(h.Name)]++
	}
	for i, h := range out.Hosts {
		if count[strings.ToLower(h.Name)] < 2 {
			continue
		}
		switch {
		case h.Name != h.Address && h.Port != 22:
			out.Hosts[i].Name = h.Name + " (" + net.JoinHostPort(h.Address, strconv.Itoa(h.Port)) + ")"
		case h.Name != h.Address:
			out.Hosts[i].Name = h.Name + " (" + h.Address + ")"
		case h.Port != 22:
			out.Hosts[i].Name = net.JoinHostPort(h.Address, strconv.Itoa(h.Port))
		}
	}
	if out.Hosts == nil {
		out.Hosts = []KnownHost{}
	}
	return out, nil
}

// splitKnownHost reads one host pattern: "host", "1.2.3.4", "2001:db8::1" or
// "[host]:port". Wildcards and negations are patterns, not servers.
func splitKnownHost(pat string) (host string, port int, ok bool) {
	pat = strings.TrimSpace(pat)
	if pat == "" || strings.ContainsAny(pat, "*?!|") {
		return "", 0, false
	}
	port = 22
	if strings.HasPrefix(pat, "[") {
		end := strings.Index(pat, "]")
		if end < 0 {
			return "", 0, false
		}
		host = pat[1:end]
		if rest := pat[end+1:]; rest != "" {
			p, err := strconv.Atoi(strings.TrimPrefix(rest, ":"))
			if err != nil || !strings.HasPrefix(rest, ":") || p < 1 || p > 65535 {
				return "", 0, false
			}
			port = p
		}
	} else {
		host = pat
	}
	if host == "" || strings.ContainsAny(host, "[] \t") {
		return "", 0, false
	}
	return host, port, true
}

func isIP(host string) bool {
	if i := strings.IndexByte(host, '%'); i >= 0 { // fe80::1%eth0
		host = host[:i]
	}
	return net.ParseIP(host) != nil
}

// plausibleKey tells a known_hosts entry from an arbitrary line of text.
func plausibleKey(keyType, data string) bool {
	if !strings.HasPrefix(keyType, "ssh-") && !strings.HasPrefix(keyType, "ecdsa-") &&
		!strings.HasPrefix(keyType, "sk-") && !strings.HasPrefix(keyType, "rsa-") {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	return err == nil && len(raw) > 0
}
