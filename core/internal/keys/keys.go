// Package keys generates, imports and loads SSH private keys. Private key
// files live in the app data directory with 0600 permissions; only metadata
// (type, fingerprint, public key) goes into the JSON store.
package keys

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

// NeedPassphraseError means the key is encrypted and no (or a wrong)
// passphrase is available. The UI answers it by prompting the user.
type NeedPassphraseError struct {
	KeyID string
	Wrong bool
}

func (e *NeedPassphraseError) Error() string {
	if e.Wrong {
		return "wrong passphrase for key " + e.KeyID
	}
	return "passphrase required for key " + e.KeyID
}

type Manager struct {
	dir     string
	store   *store.Store
	secrets *secret.Store
}

func NewManager(dir string, st *store.Store, sec *secret.Store) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Manager{dir: dir, store: st, secrets: sec}, nil
}

func (m *Manager) path(id string) string { return filepath.Join(m.dir, id) }

type GenerateRequest struct {
	Name               string `json:"name"`
	Type               string `json:"type"`
	Bits               int    `json:"bits"`
	Comment            string `json:"comment"`
	Passphrase         string `json:"passphrase"`
	RememberPassphrase bool   `json:"rememberPassphrase"`
}

func (m *Manager) Generate(req GenerateRequest) (store.Key, error) {
	priv, bits, err := newPrivateKey(req.Type, req.Bits)
	if err != nil {
		return store.Key{}, err
	}
	comment := strings.TrimSpace(req.Comment)
	if comment == "" {
		comment = defaultComment()
	}

	var block *pem.Block
	if req.Passphrase != "" {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, comment, []byte(req.Passphrase))
	} else {
		block, err = ssh.MarshalPrivateKey(priv, comment)
	}
	if err != nil {
		return store.Key{}, err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return store.Key{}, err
	}

	pub := signer.PublicKey()
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = fmt.Sprintf("%s-%s", typeName(pub), store.NewID()[:6])
	}
	k := store.Key{
		ID:          store.NewID(),
		Name:        name,
		Type:        typeName(pub),
		Bits:        bits,
		Fingerprint: ssh.FingerprintSHA256(pub),
		PublicKey:   AuthorizedLine(pub, comment),
		Comment:     comment,
		Encrypted:   req.Passphrase != "",
		Source:      "generated",
	}
	if err := writePrivate(m.path(k.ID), pem.EncodeToMemory(block)); err != nil {
		return store.Key{}, err
	}
	k, err = m.store.AddKey(k)
	if err != nil {
		os.Remove(m.path(k.ID))
		return store.Key{}, err
	}
	if req.Passphrase != "" {
		_ = m.secrets.Put(secret.KeyPassphrase(k.ID), req.Passphrase, req.RememberPassphrase)
	}
	return k, nil
}

func newPrivateKey(typ string, bits int) (crypto.PrivateKey, int, error) {
	switch strings.ToLower(typ) {
	case "", "ed25519":
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		return priv, 256, err
	case "rsa":
		if bits == 0 {
			bits = 4096
		}
		if bits != 2048 && bits != 3072 && bits != 4096 {
			return nil, 0, errors.New("RSA key size must be 2048, 3072 or 4096")
		}
		priv, err := rsa.GenerateKey(rand.Reader, bits)
		return priv, bits, err
	case "ecdsa":
		var curve elliptic.Curve
		switch bits {
		case 0, 256:
			curve, bits = elliptic.P256(), 256
		case 384:
			curve = elliptic.P384()
		case 521:
			curve = elliptic.P521()
		default:
			return nil, 0, errors.New("ECDSA key size must be 256, 384 or 521")
		}
		priv, err := ecdsa.GenerateKey(curve, rand.Reader)
		return priv, bits, err
	}
	return nil, 0, fmt.Errorf("unsupported key type %q", typ)
}

type ImportRequest struct {
	Name               string `json:"name"`
	Path               string `json:"path"`
	PEM                string `json:"pem"`
	Passphrase         string `json:"passphrase"`
	RememberPassphrase bool   `json:"rememberPassphrase"`
}

// ErrAlreadyImported is returned together with the existing key.
var ErrAlreadyImported = errors.New("key already imported")

func (m *Manager) Import(req ImportRequest) (store.Key, error) {
	var data []byte
	var err error
	source := "pasted"
	switch {
	case strings.TrimSpace(req.PEM) != "":
		data = []byte(strings.TrimSpace(req.PEM) + "\n")
	case req.Path != "":
		data, err = os.ReadFile(expandHome(req.Path))
		if err != nil {
			return store.Key{}, err
		}
		source = req.Path
	default:
		return store.Key{}, errors.New("provide a key file path or paste the private key")
	}

	info, err := inspect(data, req.Passphrase, siblingPub(req.Path))
	if err != nil {
		return store.Key{}, err
	}
	if existing, ok := m.store.KeyByFingerprint(info.fingerprint); ok {
		return existing, ErrAlreadyImported
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		if req.Path != "" {
			name = filepath.Base(req.Path)
		} else {
			name = info.typ + "-imported"
		}
	}
	comment := info.comment
	if comment == "" {
		comment = name
	}
	k := store.Key{
		ID:          store.NewID(),
		Name:        name,
		Type:        info.typ,
		Bits:        info.bits,
		Fingerprint: info.fingerprint,
		PublicKey:   AuthorizedLine(info.pub, comment),
		Comment:     comment,
		Encrypted:   info.encrypted,
		Source:      source,
	}
	if err := writePrivate(m.path(k.ID), data); err != nil {
		return store.Key{}, err
	}
	k, err = m.store.AddKey(k)
	if err != nil {
		os.Remove(m.path(k.ID))
		return store.Key{}, err
	}
	if info.encrypted && req.Passphrase != "" {
		_ = m.secrets.Put(secret.KeyPassphrase(k.ID), req.Passphrase, req.RememberPassphrase)
	}
	return k, nil
}

type keyInfo struct {
	pub         ssh.PublicKey
	typ         string
	bits        int
	fingerprint string
	comment     string
	encrypted   bool
}

// inspect parses a private key just enough to learn its public half. An
// encrypted key without passphrase is accepted as long as the public key can
// be recovered (OpenSSH format embeds it, or a sibling .pub file exists).
func inspect(data []byte, passphrase, pubPath string) (keyInfo, error) {
	var info keyInfo
	var pubComment string
	if pubPath != "" {
		if b, err := os.ReadFile(pubPath); err == nil {
			if pk, c, _, _, err := ssh.ParseAuthorizedKey(b); err == nil {
				info.pub, pubComment = pk, c
			}
		}
	}

	signer, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	switch {
	case err == nil:
		info.pub = signer.PublicKey()
	case errors.As(err, &missing):
		info.encrypted = true
		if passphrase != "" {
			s, perr := ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase))
			if perr != nil {
				return info, errors.New("wrong passphrase")
			}
			info.pub = s.PublicKey()
		} else if missing.PublicKey != nil {
			info.pub = missing.PublicKey
		} else if info.pub == nil {
			return info, errors.New("key is encrypted: enter its passphrase to import it")
		}
	default:
		return info, fmt.Errorf("not a supported private key: %w", err)
	}

	info.typ = typeName(info.pub)
	info.bits = keyBits(info.pub)
	info.fingerprint = ssh.FingerprintSHA256(info.pub)
	info.comment = pubComment
	return info, nil
}

// Signer loads a key for authentication, using a stored passphrase if needed.
func (m *Manager) Signer(k store.Key) (ssh.Signer, error) {
	data, err := os.ReadFile(m.path(k.ID))
	if err != nil {
		return nil, fmt.Errorf("key file for %q is missing: %w", k.Name, err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	var missing *ssh.PassphraseMissingError
	if err == nil {
		return signer, nil
	}
	if !errors.As(err, &missing) {
		return nil, err
	}
	pass, gerr := m.secrets.Get(secret.KeyPassphrase(k.ID))
	if gerr != nil {
		return nil, &NeedPassphraseError{KeyID: k.ID}
	}
	signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(pass))
	if err != nil {
		m.secrets.Forget(secret.KeyPassphrase(k.ID))
		return nil, &NeedPassphraseError{KeyID: k.ID, Wrong: true}
	}
	return signer, nil
}

// CheckPassphrase validates a passphrase before it is stored.
func (m *Manager) CheckPassphrase(keyID, passphrase string) error {
	data, err := os.ReadFile(m.path(keyID))
	if err != nil {
		return err
	}
	if _, err := ssh.ParsePrivateKeyWithPassphrase(data, []byte(passphrase)); err != nil {
		return &NeedPassphraseError{KeyID: keyID, Wrong: true}
	}
	return nil
}

func (m *Manager) Delete(id string) error {
	if err := m.store.DeleteKey(id); err != nil {
		return err
	}
	m.secrets.Forget(secret.KeyPassphrase(id))
	if err := os.Remove(m.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ------------------------------------------------------------ ~/.ssh scan

type Candidate struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Encrypted   bool   `json:"encrypted"`
	Imported    bool   `json:"imported"`
}

var skipNames = map[string]bool{
	"known_hosts": true, "known_hosts.old": true, "config": true,
	"authorized_keys": true, "authorized_keys2": true, "environment": true,
}

// Scan lists private keys found in ~/.ssh so they can be imported in one click.
func (m *Manager) Scan() ([]Candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Candidate{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Candidate{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || skipNames[name] || strings.HasSuffix(name, ".pub") {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.Size() > 64<<10 {
			continue
		}
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(data), "PRIVATE KEY") {
			continue
		}
		info, err := inspect(data, "", p+".pub")
		if err != nil {
			continue
		}
		_, imported := m.store.KeyByFingerprint(info.fingerprint)
		out = append(out, Candidate{
			Path: p, Name: name, Type: info.typ, Fingerprint: info.fingerprint,
			Encrypted: info.encrypted, Imported: imported,
		})
	}
	return out, nil
}

// ------------------------------------------------------------ helpers

// AuthorizedLine renders a public key as a single authorized_keys line.
func AuthorizedLine(pub ssh.PublicKey, comment string) string {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	comment = strings.Join(strings.Fields(comment), " ")
	if comment != "" {
		line += " " + comment
	}
	return line
}

func typeName(pub ssh.PublicKey) string {
	switch t := pub.Type(); {
	case t == ssh.KeyAlgoED25519:
		return "ed25519"
	case t == ssh.KeyAlgoRSA:
		return "rsa"
	case strings.HasPrefix(t, "ecdsa-"):
		return "ecdsa"
	case strings.HasPrefix(t, "sk-ssh-ed25519"):
		return "ed25519-sk"
	case strings.HasPrefix(t, "sk-ecdsa"):
		return "ecdsa-sk"
	default:
		return t
	}
}

func keyBits(pub ssh.PublicKey) int {
	cp, ok := pub.(ssh.CryptoPublicKey)
	if !ok {
		return 0
	}
	switch k := cp.CryptoPublicKey().(type) {
	case *rsa.PublicKey:
		return k.N.BitLen()
	case *ecdsa.PublicKey:
		return k.Curve.Params().BitSize
	case ed25519.PublicKey:
		return 256
	}
	return 0
}

func writePrivate(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func siblingPub(path string) string {
	if path == "" {
		return ""
	}
	return expandHome(path) + ".pub"
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func defaultComment() string {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	host, _ := os.Hostname()
	if user == "" {
		user = "termward"
	}
	if host == "" {
		return user
	}
	return user + "@" + host
}
