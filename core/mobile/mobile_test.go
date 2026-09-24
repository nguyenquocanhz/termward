package mobile

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestStartServesAuthenticatedAPI(t *testing.T) {
	port, err := Start(t.TempDir(), strings.Repeat("ab", 32), "vi", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer Stop()

	if again, _ := Start(t.TempDir(), strings.Repeat("ab", 32), "vi", nil); again != port {
		t.Errorf("second Start should reuse the running core: %d vs %d", again, port)
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/hosts", port), nil)
	req.Header.Set("Authorization", "Bearer "+Token())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestStartRejectsBadVaultKey(t *testing.T) {
	if _, err := Start(t.TempDir(), "short", "en", nil); err == nil {
		Stop()
		t.Fatal("a short vault key must be rejected")
	}
}
