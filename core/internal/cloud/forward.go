package cloud

import (
	"fmt"
	"strings"

	"github.com/nguyenquocanhz/termward/core/internal/health"
)

// Forwarding is what the user chose to send to their channels.
type Forwarding struct {
	Critical   bool   `json:"critical"`   // critical problems and servers going down
	Warnings   bool   `json:"warnings"`   // warnings
	Recoveries bool   `json:"recoveries"` // back to healthy, hardware problems resolved
	Lang       string `json:"lang"`       // language of the messages: "vi" or "en"
}

func DefaultForwarding() Forwarding {
	return Forwarding{Critical: true, Warnings: true, Recoveries: true, Lang: "vi"}
}

func normLang(l string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "vi") {
		return "vi"
	}
	return "en"
}

// allows reports whether events of this contract level are forwarded.
func (f Forwarding) allows(level string) bool {
	switch level {
	case "crit":
		return f.Critical
	case "warn":
		return f.Warnings
	case "ok":
		return f.Recoveries
	}
	return false
}

// eventLevel maps an alert onto the contract levels: down and critical are
// "crit", recoveries "ok". Anything else is not forwarded.
func eventLevel(a health.Alert) string {
	switch a.To {
	case health.LevelDown, health.LevelCrit:
		return "crit"
	case health.LevelWarn:
		return "warn"
	case health.LevelOK:
		return "ok"
	}
	return ""
}

const (
	titleMax = 200
	bodyMax  = 2000
	maxLines = 10
)

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// toEvent converts an alert (health or hardware) into a contract event. The
// id derives from the alert id, which the monitor assigns once per alert, so
// retries of the same alert are deduplicated by the server. Texts are the
// ones Termward already uses for notifications.
func toEvent(a health.Alert, address string) (Event, bool) {
	level := eventLevel(a)
	if level == "" || a.ID == "" {
		return Event{}, false
	}
	kind := "health"
	if a.Kind == "hardware" {
		kind = "hardware"
	}
	titleEN, bodyEN := health.AlertText(a, "en")
	titleVI, bodyVI := health.AlertText(a, "vi")
	if kind == "hardware" {
		if en, vi, ok := hardwareLines(a.Hardware); ok {
			bodyEN, bodyVI = en, vi
		}
	} else if en, vi, ok := findingLines(a.Findings); ok {
		bodyEN, bodyVI = en, vi
	}
	if bodyEN == "" && a.Error != "" && level != "ok" {
		bodyEN, bodyVI = a.Error, a.Error
	}
	name := a.HostName
	if name == "" {
		name = a.HostID
	}
	return Event{
		ID:    "tw-" + a.ID,
		Kind:  kind,
		Level: level,
		Host:  EventHost{Name: cut(name, 253), Address: cut(address, 253)},
		Title: Text{EN: cut(titleEN, titleMax), VI: cut(titleVI, titleMax)},
		Body:  Text{EN: cut(bodyEN, bodyMax), VI: cut(bodyVI, bodyMax)},
		At:    a.At.UTC(),
	}, true
}

// findingLines lists every finding that needs attention, one per line (the
// notification text shows only the first).
func findingLines(fs []health.Finding) (string, string, bool) {
	var en, vi []string
	for _, f := range fs {
		if f.Level == health.LevelInfo {
			continue
		}
		en = append(en, health.FindingText(f, "en"))
		vi = append(vi, health.FindingText(f, "vi"))
	}
	if len(en) <= 1 {
		return "", "", false // AlertText already gives the single line
	}
	return joinLines(en, "en"), joinLines(vi, "vi"), true
}

// hardwareLines lists every hardware change, one per line.
func hardwareLines(cs []health.HardwareChange) (string, string, bool) {
	if len(cs) <= 1 {
		return "", "", false
	}
	var en, vi []string
	for _, c := range cs {
		en = append(en, changeLine(c, "en"))
		vi = append(vi, changeLine(c, "vi"))
	}
	return joinLines(en, "en"), joinLines(vi, "vi"), true
}

func changeLine(c health.HardwareChange, lang string) string {
	t := c.Title.In(lang)
	if c.Target != "" && !strings.Contains(t, c.Target) {
		t += " (" + c.Target + ")"
	}
	return t
}

func joinLines(lines []string, lang string) string {
	more := 0
	if len(lines) > maxLines {
		more = len(lines) - maxLines
		lines = lines[:maxLines]
	}
	out := "• " + strings.Join(lines, "\n• ")
	if more > 0 {
		if lang == "vi" {
			out += fmt.Sprintf("\n… và %d mục khác", more)
		} else {
			out += fmt.Sprintf("\n… and %d more", more)
		}
	}
	return out
}
