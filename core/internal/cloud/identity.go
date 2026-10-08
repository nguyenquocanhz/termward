package cloud

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
)

// Secrets is the part of secret.Store the cloud client uses. Device keys are
// stored with remember=true, i.e. in the OS keychain (or the encrypted vault
// file on mobile), never in the JSON data files.
type Secrets interface {
	Get(name string) (string, error)
	Put(name, value string, remember bool) error
	Forget(name string)
}

// deviceKeyName is the secret holding the device key of one install. The
// install id keeps two data directories on one machine (e.g. a development
// core next to the real app) from sharing, or deleting, each other's key.
func deviceKeyName(installID string) string { return "cloud/" + installID + "/device-key" }

var errNoKey = errors.New("no device key")

// loadKey returns the device key from the secret store (errNoKey when there
// is none or it is unreadable).
func loadKey(sec Secrets, name string) (ed25519.PrivateKey, error) {
	v, err := sec.Get(name)
	if err != nil || v == "" {
		return nil, errNoKey
	}
	seed, err := b64.DecodeString(v)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errNoKey
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// createKey makes a new key pair and stores its seed in the secret store. If
// the store cannot keep it (no keychain service, e.g. a headless Linux box),
// nothing is kept: a key that disappears on restart would leave a device on
// the account that can never sign again.
func createKey(sec Secrets, name string) (ed25519.PrivateKey, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	if err := sec.Put(name, b64.EncodeToString(seed), true); err != nil {
		sec.Forget(name)
		return nil, secretStoreError(err)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func loadOrCreateKey(sec Secrets, name string) (ed25519.PrivateKey, error) {
	if k, err := loadKey(sec, name); err == nil {
		return k, nil
	}
	return createKey(sec, name)
}

func deleteKey(sec Secrets, name string) { sec.Forget(name) }

func secretStoreError(err error) *Error {
	return &Error{
		Status: 500, Code: "secret_store_unavailable",
		Message: fmt.Sprintf("the OS secret store cannot keep the device key: %v", err),
	}
}
