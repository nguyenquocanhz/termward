// Package mobile is the gomobile binding the Android and iOS apps embed:
//
//	gomobile bind -target=android -o termwardcore.aar ./mobile
//	gomobile bind -target=ios     -o Termwardcore.xcframework ./mobile
//
// The app calls Start, then points its WebView UI at http://127.0.0.1:<port>
// with Token(), exactly like the desktop app does with termwardd.
package mobile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"

	"github.com/nguyenquocanhz/termward/core/internal/app"
	"github.com/nguyenquocanhz/termward/core/internal/health"
	"github.com/nguyenquocanhz/termward/core/internal/secret"
)

// Version is overridden at build time with -ldflags "-X .../mobile.version=".
var version = "dev"

// Notifier is implemented natively to post an OS notification. It is called
// from a background goroutine for every health alert, even while the UI is
// paused.
type Notifier interface {
	Post(title, body, hostID string)
}

var (
	mu    sync.Mutex
	inst  *app.Instance
	token string
	lang  = "en"
)

// Start runs the core. vaultKeyHex is a 32-byte key (hex) that the app keeps
// in the platform keystore; it encrypts remembered passwords/passphrases.
// Returns the loopback port the UI should connect to.
func Start(dataDir, vaultKeyHex, language string, n Notifier) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	if inst != nil {
		return inst.Port, nil
	}
	key, err := hex.DecodeString(vaultKeyHex)
	if err != nil || len(key) != 32 {
		return 0, errors.New("vault key must be 64 hex characters")
	}
	vault, err := secret.NewFileBackend(filepath.Join(dataDir, "secrets.bin"), key)
	if err != nil {
		return 0, err
	}
	if language != "" {
		lang = language
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token = hex.EncodeToString(b)

	cfg := app.Config{
		DataDir: dataDir,
		Addr:    "127.0.0.1:0",
		Token:   token,
		Version: version,
		Secrets: vault,
	}
	if n != nil {
		cfg.OnAlert = func(a health.Alert) {
			mu.Lock()
			l := lang
			mu.Unlock()
			title, body := health.AlertText(a, l)
			n.Post(title, body, a.HostID)
		}
	}
	i, err := app.Start(context.Background(), cfg)
	if err != nil {
		return 0, err
	}
	inst = i
	return i.Port, nil
}

// Token is the bearer token the UI must send (valid after Start).
func Token() string {
	mu.Lock()
	defer mu.Unlock()
	return token
}

// SetLanguage switches native notification text ("en" or "vi").
func SetLanguage(language string) {
	mu.Lock()
	lang = language
	mu.Unlock()
}

// Stop closes every connection and the local server.
func Stop() {
	mu.Lock()
	i := inst
	inst = nil
	mu.Unlock()
	if i != nil {
		i.Stop()
	}
}

func Version() string { return version }
