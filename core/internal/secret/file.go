package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// FileBackend keeps secrets in one AES-256-GCM encrypted file. Mobile apps use
// it with a key held by the platform keystore (Android Keystore / iOS
// Keychain), since there is no desktop-style keychain service to call.
type FileBackend struct {
	mu   sync.Mutex
	path string
	aead cipher.AEAD
	data map[string]string
}

func NewFileBackend(path string, key []byte) (*FileBackend, error) {
	if len(key) != 32 {
		return nil, errors.New("secret file key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	f := &FileBackend{path: path, aead: aead, data: map[string]string{}}

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	ns := aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("secret file is corrupt")
	}
	plain, err := aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return nil, errors.New("secret file cannot be decrypted with this key")
	}
	if err := json.Unmarshal(plain, &f.data); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *FileBackend) Get(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.data[name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *FileBackend) Set(name, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[name] = value
	return f.saveLocked()
}

func (f *FileBackend) Delete(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[name]; !ok {
		return nil
	}
	delete(f.data, name)
	return f.saveLocked()
}

func (f *FileBackend) saveLocked() error {
	plain, err := json.Marshal(f.data)
	if err != nil {
		return err
	}
	nonce := make([]byte, f.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	out := f.aead.Seal(nonce, nonce, plain, nil)
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}
