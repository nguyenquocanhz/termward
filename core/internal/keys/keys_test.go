package keys

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/nguyenquocanhz/termward/core/internal/secret"
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

func newManager(t *testing.T) (*Manager, *store.Store, *secret.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sec := secret.NewWithBackend(secret.NewMemory())
	m, err := NewManager(filepath.Join(dir, "keys"), st, sec)
	if err != nil {
		t.Fatal(err)
	}
	return m, st, sec
}

func TestGenerateAllTypes(t *testing.T) {
	m, _, _ := newManager(t)
	cases := []struct {
		typ  string
		bits int
		want string
	}{
		{"ed25519", 0, "ssh-ed25519 "},
		{"rsa", 3072, "ssh-rsa "},
		{"ecdsa", 384, "ecdsa-sha2-nistp384 "},
	}
	for _, c := range cases {
		k, err := m.Generate(GenerateRequest{Type: c.typ, Bits: c.bits, Comment: "me@laptop"})
		if err != nil {
			t.Fatalf("%s: %v", c.typ, err)
		}
		if !strings.HasPrefix(k.PublicKey, c.want) || !strings.HasSuffix(k.PublicKey, " me@laptop") {
			t.Errorf("%s public key line = %q", c.typ, k.PublicKey)
		}
		if !strings.HasPrefix(k.Fingerprint, "SHA256:") || k.Encrypted {
			t.Errorf("%s metadata = %+v", c.typ, k)
		}
		if _, err := m.Signer(k); err != nil {
			t.Errorf("%s signer: %v", c.typ, err)
		}
	}
	if _, err := m.Generate(GenerateRequest{Type: "rsa", Bits: 1024}); err == nil {
		t.Error("1024-bit RSA must be rejected")
	}
}

func TestEncryptedKeyNeedsPassphrase(t *testing.T) {
	m, _, sec := newManager(t)
	k, err := m.Generate(GenerateRequest{Type: "ed25519", Passphrase: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if !k.Encrypted {
		t.Fatal("key should be marked encrypted")
	}
	// Passphrase is kept for the session only (not remembered).
	if _, err := m.Signer(k); err != nil {
		t.Fatalf("session passphrase should unlock the key: %v", err)
	}

	sec.Forget(secret.KeyPassphrase(k.ID))
	var need *NeedPassphraseError
	if _, err := m.Signer(k); !errors.As(err, &need) || need.Wrong {
		t.Fatalf("want NeedPassphraseError, got %v", err)
	}
	if err := m.CheckPassphrase(k.ID, "nope"); !errors.As(err, &need) || !need.Wrong {
		t.Fatalf("wrong passphrase not detected: %v", err)
	}
	if err := m.CheckPassphrase(k.ID, "s3cret"); err != nil {
		t.Fatal(err)
	}
}

func TestImportAndDedupe(t *testing.T) {
	src, _, _ := newManager(t)
	orig, err := src.Generate(GenerateRequest{Type: "ed25519", Passphrase: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := os.ReadFile(src.path(orig.ID))
	if err != nil {
		t.Fatal(err)
	}

	m, _, _ := newManager(t)
	// Encrypted OpenSSH keys embed the public key, so no passphrase is needed.
	k, err := m.Import(ImportRequest{PEM: string(pemBytes), Name: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if k.Fingerprint != orig.Fingerprint || !k.Encrypted || k.Name != "laptop" {
		t.Errorf("imported = %+v", k)
	}
	if _, err := m.Import(ImportRequest{PEM: string(pemBytes)}); !errors.Is(err, ErrAlreadyImported) {
		t.Errorf("second import: %v", err)
	}
	if _, err := m.Import(ImportRequest{PEM: "not a key"}); err == nil {
		t.Error("garbage must be rejected")
	}
}

func TestDeleteKeyInUse(t *testing.T) {
	m, st, _ := newManager(t)
	k, err := m.Generate(GenerateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveHost(store.Host{Name: "db", Address: "db.local", User: "root", Auth: store.AuthKey, KeyID: k.ID}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(k.ID); err == nil || !strings.Contains(err.Error(), "db") {
		t.Fatalf("deleting a key in use should name the host, got %v", err)
	}
}

func TestAuthorizedLineRoundTrip(t *testing.T) {
	m, _, _ := newManager(t)
	k, _ := m.Generate(GenerateRequest{Comment: "a  b\tc"})
	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(k.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if comment != "a b c" || ssh.FingerprintSHA256(pub) != k.Fingerprint {
		t.Errorf("comment %q fingerprint %s", comment, ssh.FingerprintSHA256(pub))
	}
}
