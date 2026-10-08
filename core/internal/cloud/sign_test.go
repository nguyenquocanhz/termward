package cloud

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nguyenquocanhz/termward/core/internal/secret"
)

// The example vectors of docs/API.md (RFC 8032 test key 1, public).
const (
	vecSeed      = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	vecPublicKey = "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"
	vecDevice    = "dev_Yk3q9Zr0TnS2pX7bV1cW4A"
	vecTime      = 1759480000
	vecNonce     = "AAECAwQFBgcICQoLDA0ODw"
	vecMachine   = "3f9c2b7e1a4d8c6f0e5b9a2d7c4f1e8b"
	vecBody      = `{"months":1}`
	vecCanonical = "TW2\nPOST\n/v1/checkout\ndev_Yk3q9Zr0TnS2pX7bV1cW4A\n1759480000\nAAECAwQFBgcICQoLDA0ODw\n" +
		"3f9c2b7e1a4d8c6f0e5b9a2d7c4f1e8b\ne84b682dcdd7ae9602d2db5e01ca07790184617d31e0fda05f211dc3aab837aa"
	vecSignature    = "0h2rAypnS4PgBawIGs_hM00GaXt21Bwu47Bkk5VLBTGVfXvdu6n9iRfyo9iQgJevJKsDduhryzKZnAtxAR_-DA"
	vecVerifyProof  = "LJEZQSpjJDPgyEPULFqwflDCKNBcmJUeJvh2XgGzhipsdIGY5lVyvBCZzkrZAvGu6O8-6OyKBiSi7ZxbmwHxAA"
	vecReplaceToken = "tq3Bn1o9cX0dYwq0m2l2bVZf3m8xR1sUu8qkq5hYb4E"
	vecReplaceProof = "UF5sruzGKif7CLarKE0w7JYRwNrdQMzgJFzEPTaV3DrPKoFNRHCc1Su2tKpTEAh4nhPjXJb1nU5PSgxiYvWHCA"
)

func vecKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed, err := hex.DecodeString(vecSeed)
	if err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func vecNonceBytes() []byte {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func TestContractSigningVector(t *testing.T) {
	key := vecKey(t)
	if got := PublicKeyString(key); got != vecPublicKey {
		t.Fatalf("public key %q, want %q", got, vecPublicKey)
	}
	if got := b64.EncodeToString(vecNonceBytes()); got != vecNonce {
		t.Fatalf("nonce %q, want %q", got, vecNonce)
	}
	got := Canonical("post", "/v1/checkout", vecDevice, "1759480000", vecNonce, vecMachine, []byte(vecBody))
	if got != vecCanonical {
		t.Fatalf("canonical string:\n%s\nwant:\n%s", got, vecCanonical)
	}
	s := Sign(key, "POST", "/v1/checkout", vecDevice, vecMachine, []byte(vecBody), time.Unix(vecTime, 0), vecNonceBytes())
	if s.Signature != vecSignature {
		t.Fatalf("signature\n%s\nwant\n%s", s.Signature, vecSignature)
	}
	if s.Time != "1759480000" || s.Nonce != vecNonce || s.Device != vecDevice || s.Machine != vecMachine {
		t.Fatalf("headers %+v", s)
	}
	if strings.Contains(s.Signature, "=") {
		t.Fatal("signature must be unpadded base64url")
	}
}

// The request body the client marshals for a checkout must be exactly the
// bytes of the vector, or the body hash differs.
func TestCheckoutBodyBytesMatchVector(t *testing.T) {
	b, err := jsonBody(map[string]int{"months": 1})
	if err != nil || string(b) != vecBody {
		t.Fatalf("body %q (%v), want %q", b, err, vecBody)
	}
}

func TestBodyHashOfEmptyBody(t *testing.T) {
	if got := BodyHash(nil); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty body hash %s", got)
	}
}

func TestContractProofVectors(t *testing.T) {
	key := vecKey(t)
	wantMsg := "TW2-REGISTER\na@b.vn\n" + vecPublicKey + "\n" + vecMachine
	if got := RegistrationMessage("a@b.vn", vecPublicKey, vecMachine); got != wantMsg {
		t.Fatalf("message %q", got)
	}
	if got := Proof(key, "a@b.vn", vecPublicKey, vecMachine); got != vecVerifyProof {
		t.Fatalf("verify proof\n%s\nwant\n%s", got, vecVerifyProof)
	}
	if got := Proof(key, vecReplaceToken, vecPublicKey, vecMachine); got != vecReplaceProof {
		t.Fatalf("replace proof\n%s\nwant\n%s", got, vecReplaceProof)
	}
}

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{
		" An@Example.VN ": "an@example.vn",
		"a@b.vn":          "a@b.vn",
	} {
		got, ok := NormalizeEmail(in)
		if !ok || got != want {
			t.Errorf("%q -> %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "a", "a@b", "a@@b.vn", "a b@c.vn", "a@-b.vn", strings.Repeat("a", 250) + "@b.vn"} {
		if _, ok := NormalizeEmail(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFingerprint(t *testing.T) {
	// printf 'termward-machine:%s' b08e8f5c-1c9a-4f43-8a39-0f2c0d6c8a11 | sha256sum
	got := Fingerprint("b08e8f5c-1c9a-4f43-8a39-0f2c0d6c8a11")
	if got != "b85bd4a50f1c146ec12a43a3913f05c4" {
		t.Fatalf("fingerprint %s", got)
	}
}

// failingSecrets is a secret store whose persistent backend refuses writes.
type failingSecrets struct{ *secret.Store }

func (f failingSecrets) Put(name, value string, remember bool) error {
	_ = f.Store.Put(name, value, false)
	return errors.New("no keychain")
}

func TestResolveMachineID(t *testing.T) {
	sec := secret.NewWithBackend(secret.NewMemory())
	sys := func() (string, error) { return " sys-id\n", nil }
	none := func() (string, error) { return "", errNoMachineID }

	if id, src, _ := resolveMachineID(" native-id ", sys, sec); id != "native-id" || src != MachineNative {
		t.Errorf("native: %q %q", id, src)
	}
	if id, src, _ := resolveMachineID("", sys, sec); id != "sys-id" || src != MachineSystem {
		t.Errorf("system: %q %q", id, src)
	}
	id1, src, err := resolveMachineID("", none, sec)
	if err != nil || src != MachineGenerated || len(id1) != 32 {
		t.Fatalf("generated: %q %q %v", id1, src, err)
	}
	if id2, _, _ := resolveMachineID("", none, sec); id2 != id1 {
		t.Errorf("generated id must be stable: %q then %q", id1, id2)
	}
	if v, _ := sec.Get(secretMachineID); v != id1 {
		t.Errorf("generated id not kept in the secret store")
	}

	bad := failingSecrets{secret.NewWithBackend(secret.NewMemory())}
	_, _, err = resolveMachineID("", none, bad)
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != "secret_store_unavailable" {
		t.Fatalf("want secret_store_unavailable, got %v", err)
	}
	if _, err := bad.Get(secretMachineID); err == nil {
		t.Error("an id that could not be persisted must not linger in the session")
	}
}

func TestMachineReaders(t *testing.T) {
	files := map[string]string{"/var/lib/dbus/machine-id": "dbus-id\n", "/etc/machine-id": "  \n"}
	read := func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("missing")
	}
	if v, err := readFirstFile(read, "/etc/machine-id", "/var/lib/dbus/machine-id"); err != nil || v != "dbus-id" {
		t.Errorf("fallback file: %q %v", v, err)
	}
	files["/etc/machine-id"] = "etc-id\n"
	if v, _ := readFirstFile(read, "/etc/machine-id", "/var/lib/dbus/machine-id"); v != "etc-id" {
		t.Errorf("first file: %q", v)
	}
	if _, err := readFirstFile(read, "/nope"); err == nil {
		t.Error("no file must be an error")
	}

	out := `+-o J314sAP  <class IOPlatformExpertDevice, id 0x100000226, registered, matched, active, busy 0 (88 ms), retain 39>
    {
      "IOPlatformSerialNumber" = "C02XXXXXXX"
      "IOPlatformUUID" = "4C4C4544-0035-3010-8048-B4C04F4E3732"
    }`
	if v, err := parseIoreg(out); err != nil || v != "4C4C4544-0035-3010-8048-B4C04F4E3732" {
		t.Errorf("ioreg: %q %v", v, err)
	}
	if _, err := parseIoreg("nothing here"); err == nil {
		t.Error("missing IOPlatformUUID must be an error")
	}
}

func TestSystemMachineIDOnThisOS(t *testing.T) {
	// Not every CI box has a machine id; when there is one it must be stable.
	a, err := systemMachineID()
	if err != nil {
		t.Skipf("no system machine id here: %v", err)
	}
	b, _ := systemMachineID()
	if a == "" || a != b {
		t.Fatalf("unstable machine id %q / %q", a, b)
	}
}

func TestResolveBaseURL(t *testing.T) {
	ok := map[string]string{
		"":                            DefaultBaseURL,
		"https://example.workers.dev": "https://example.workers.dev",
		"https://example.dev/":        "https://example.dev",
		"http://127.0.0.1:8787":       "http://127.0.0.1:8787",
		"http://localhost:8787":       "http://localhost:8787",
		"http://[::1]:8787":           "http://[::1]:8787",
	}
	for in, want := range ok {
		got, err := ResolveBaseURL(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"http://10.0.0.5:8787", "http://example.com", "ftp://x", "https://example.dev/prefix",
		"https://user:pw@example.dev", "https://example.dev/?a=1", "not a url",
	} {
		got, err := ResolveBaseURL(bad)
		if err == nil || got != DefaultBaseURL {
			t.Errorf("%q must be refused (got %q, %v)", bad, got, err)
		}
	}
}

func TestDeviceKeyLivesInSecretStore(t *testing.T) {
	sec := secret.NewWithBackend(secret.NewMemory())
	kn := deviceKeyName("a1b2")
	if kn != "cloud/a1b2/device-key" {
		t.Fatal(kn)
	}
	if _, err := loadKey(sec, kn); err == nil {
		t.Fatal("no key yet")
	}
	k1, err := loadOrCreateKey(sec, kn)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := loadOrCreateKey(sec, kn)
	if err != nil || !k1.Equal(k2) {
		t.Fatal("the key must be reused")
	}
	deleteKey(sec, kn)
	if _, err := loadKey(sec, kn); err == nil {
		t.Fatal("key must be gone after delete")
	}
	bad := failingSecrets{secret.NewWithBackend(secret.NewMemory())}
	if _, err := createKey(bad, kn); err == nil {
		t.Fatal("a key that cannot be persisted must be refused")
	}
	if _, err := loadKey(bad, kn); err == nil {
		t.Fatal("an unpersisted key must not linger in the session")
	}
}
