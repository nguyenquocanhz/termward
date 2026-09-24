package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Planned reboots and shutdowns should not page anyone.
const (
	rebootMute   = 10 * time.Minute
	poweroffMute = 12 * time.Hour
)

// powerHost reboots or shuts down a server. It needs root or sudo; the sudo
// password (if any) is sent only inside the encrypted SSH session.
func (s *Server) powerHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Action       string `json:"action"` // reboot | poweroff
		SudoPassword string `json:"sudoPassword"`
	}
	if !decode(w, r, &in) {
		return
	}
	var cmd string
	var mute time.Duration
	switch in.Action {
	case "reboot":
		cmd, mute = "systemctl reboot || reboot || shutdown -r now", rebootMute
	case "poweroff":
		cmd, mute = "systemctl poweroff || poweroff || shutdown -h now", poweroffMute
	default:
		writeError(w, http.StatusBadRequest, "invalid", "action must be reboot or poweroff", nil)
		return
	}
	if _, err := s.store.Host(id); err != nil {
		fail(w, err, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.pool.Run(ctx, id, "sh -s", strings.NewReader(powerScript(cmd, in.SudoPassword)))
	if err != nil {
		fail(w, err, http.StatusBadGateway)
		return
	}
	switch res.ExitCode {
	case 0:
	case 77:
		writeError(w, http.StatusForbidden, "sudo_required", "this needs root or sudo: enter the sudo password", nil)
		return
	case 78:
		writeError(w, http.StatusForbidden, "sudo_wrong", "the sudo password was rejected", nil)
		return
	default:
		writeError(w, http.StatusBadGateway, "power_failed", strings.TrimSpace(res.Stderr), nil)
		return
	}

	s.monitor.Mute(id, time.Now().Add(mute))
	// The server drops the connection when it goes down; forget it now.
	go func() {
		time.Sleep(4 * time.Second)
		s.pool.Drop(id)
	}()
	writeJSON(w, http.StatusOK, map[string]any{"action": in.Action, "mutedUntil": time.Now().Add(mute).UTC()})
}

// powerScript runs cmd as root in the background (so the SSH session returns
// cleanly) after checking we actually have root: directly, via passwordless
// sudo, or via sudo with the given password.
func powerScript(cmd, sudoPassword string) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	return `CMD=` + q("sleep 2; "+cmd) + `
PW=` + q(sudoPassword) + `
if [ "$(id -u)" = 0 ]; then
	nohup sh -c "$CMD" </dev/null >/dev/null 2>&1 &
elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
	nohup sudo -n sh -c "$CMD" </dev/null >/dev/null 2>&1 &
elif [ -n "$PW" ] && command -v sudo >/dev/null 2>&1; then
	printf '%s\n' "$PW" | sudo -S -k -p '' true 2>/dev/null || exit 78
	(printf '%s\n' "$PW" | nohup sudo -S -p '' sh -c "$CMD" >/dev/null 2>&1 &)
else
	exit 77
fi
echo scheduled
`
}
