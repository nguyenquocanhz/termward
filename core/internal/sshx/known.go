package sshx

import (
	"bufio"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// UnknownHostError is returned the first time we see a host. The UI shows the
// fingerprint and, once the user confirms, calls KnownHosts.Trust.
type UnknownHostError struct {
	HostID      string `json:"hostId"`
	Address     string `json:"address"`
	KeyType     string `json:"keyType"`
	Fingerprint string `json:"fingerprint"`
}

func (e *UnknownHostError) Error() string {
	return fmt.Sprintf("unknown host %s (%s %s)", e.Address, e.KeyType, e.Fingerprint)
}

// HostKeyChangedError means the server presented a different key than the one
// we trusted before — possibly a man-in-the-middle attack.
type HostKeyChangedError struct {
	HostID      string `json:"hostId"`
	Address     string `json:"address"`
	KeyType     string `json:"keyType"`
	Fingerprint string `json:"fingerprint"`
}

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf("host key for %s has changed (now %s %s)", e.Address, e.KeyType, e.Fingerprint)
}

type pendingKey struct {
	address string
	key     ssh.PublicKey
}

// KnownHosts verifies host keys against the app's own known_hosts file first
// and the user's ~/.ssh/known_hosts second, so hosts already trusted in
// OpenSSH connect without a prompt. Termward only ever writes its own file.
type KnownHosts struct {
	path  string
	extra []string

	mu      sync.Mutex
	pending map[string]pendingKey
}

func NewKnownHosts(path string, extra ...string) (*KnownHosts, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	return &KnownHosts{path: path, extra: extra, pending: map[string]pendingKey{}}, nil
}

func (k *KnownHosts) files() []string {
	files := []string{k.path}
	for _, f := range k.extra {
		if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
			files = append(files, f)
		}
	}
	return files
}

func (k *KnownHosts) Callback(hostID string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		k.mu.Lock()
		cb, err := knownhosts.New(k.files()...)
		k.mu.Unlock()
		if err != nil {
			return fmt.Errorf("read known_hosts: %w", err)
		}
		err = cb(hostname, remote, key)
		var ke *knownhosts.KeyError
		if !errors.As(err, &ke) {
			return err
		}
		k.mu.Lock()
		k.pending[hostID] = pendingKey{address: hostname, key: key}
		k.mu.Unlock()
		if len(ke.Want) == 0 {
			return &UnknownHostError{HostID: hostID, Address: hostname, KeyType: key.Type(), Fingerprint: ssh.FingerprintSHA256(key)}
		}
		return &HostKeyChangedError{HostID: hostID, Address: hostname, KeyType: key.Type(), Fingerprint: ssh.FingerprintSHA256(key)}
	}
}

// dummyKey is never a real host key; checking it reveals which key types are
// already known for an address.
var dummyKey = func() ssh.PublicKey {
	pub := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public()
	k, _ := ssh.NewPublicKey(pub)
	return k
}()

// HostKeyAlgorithms returns the algorithms of keys we already trust for the
// address, so the server is asked for a key we can verify (as OpenSSH does).
// Empty means the host is unknown and any algorithm is fine.
func (k *KnownHosts) HostKeyAlgorithms(address string) []string {
	k.mu.Lock()
	cb, err := knownhosts.New(k.files()...)
	k.mu.Unlock()
	if err != nil {
		return nil
	}
	_, portStr, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portStr)
	err = cb(address, &net.TCPAddr{IP: net.IPv4zero, Port: port}, dummyKey)
	var ke *knownhosts.KeyError
	if !errors.As(err, &ke) {
		return nil
	}
	var algos []string
	for _, w := range ke.Want {
		switch t := w.Key.Type(); t {
		case ssh.KeyAlgoRSA:
			algos = append(algos, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA)
		default:
			algos = append(algos, t)
		}
	}
	return algos
}

// Trust records the key last presented by the host, replacing any older key
// for the same address in Termward's own file.
func (k *KnownHosts) Trust(hostID, fingerprint string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	pk, ok := k.pending[hostID]
	if !ok || ssh.FingerprintSHA256(pk.key) != fingerprint {
		return errors.New("host key is no longer pending — connect again")
	}

	norm := knownhosts.Normalize(pk.address)
	var kept []string
	if f, err := os.Open(k.path); err == nil {
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !lineMatchesHost(line, norm) {
				kept = append(kept, line)
			}
		}
		f.Close()
	}
	kept = append(kept, knownhosts.Line([]string{norm}, pk.key))

	tmp := k.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(kept, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, k.path); err != nil {
		return err
	}
	delete(k.pending, hostID)
	return nil
}

func lineMatchesHost(line, norm string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
		return false
	}
	hosts := fields[0]
	if strings.HasPrefix(hosts, "@") && len(fields) > 1 {
		hosts = fields[1]
	}
	for _, h := range strings.Split(hosts, ",") {
		if h == norm {
			return true
		}
	}
	return false
}
