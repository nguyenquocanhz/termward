package api

import (
	"fmt"
	"strings"

	"github.com/nguyenquocanhz/diagward/model"

	"github.com/nguyenquocanhz/termward/core/internal/health"
)

// Hardware alerts compare each check with the previous saved result of the
// same host. A finding is identified by its Diagward rule id and target
// ("disk.smart_failed" on "/dev/sda"); its severity decides whether it is new,
// worse or resolved.

const (
	changeNew      = "new"
	changeWorse    = "worse"
	changeResolved = "resolved"
)

func findingKey(f model.Finding) string { return f.ID + "\x00" + f.Target }

// worstFindings indexes a report's findings by key, keeping the most severe
// one per key, and returns the keys in report order (most severe first).
func worstFindings(r *model.Report) (map[string]model.Finding, []string) {
	m := map[string]model.Finding{}
	var order []string
	if r == nil {
		return m, nil
	}
	for _, f := range r.Findings {
		k := findingKey(f)
		old, ok := m[k]
		if !ok {
			order = append(order, k)
		}
		if !ok || f.Severity > old.Severity {
			m[k] = f
		}
	}
	return m, order
}

func componentChecked(r *model.Report, comp string) bool {
	for _, c := range r.Summary {
		if c.Component == comp {
			return c.Checked
		}
	}
	return false
}

func privileged(res *HardwareResult) bool { return res.RanAs != "user" }

// diffHardware lists what changed between two checks of one host:
//
//   - new: a Warn/Crit finding that was absent or below Warn before;
//   - worse: a finding that went from Warn to Crit;
//   - resolved: a Crit finding that is gone (or no longer a problem).
//
// The first check of a host (prev == nil) reports only its Crit findings, so
// adding a server does not page anyone about wear and tear it always had; the
// same goes for a component the previous check did not cover.
// A result collected without root cannot be compared with one collected as
// root: checks that need root are missing, which says nothing about whether a
// problem went away (and a later root check would make old problems look new).
func diffHardware(prev, cur *HardwareResult) []health.HardwareChange {
	if cur == nil || cur.Report == nil {
		return nil
	}
	curF, curOrder := worstFindings(cur.Report)
	var prevF map[string]model.Finding
	var prevOrder []string
	if prev != nil {
		prevF, prevOrder = worstFindings(prev.Report)
	}
	baseline := prev != nil && prev.Report != nil && (privileged(prev) || !privileged(cur))

	var out []health.HardwareChange
	for _, k := range curOrder {
		f := curF[k]
		if f.Severity < model.Warn {
			continue
		}
		p, had := prevF[k]
		switch {
		// A component the previous check did not look at (cut off early, a
		// tool missing) has no baseline either: treat it like a first check.
		case !baseline || !componentChecked(prev.Report, f.Component):
			if f.Severity == model.Crit && (!had || p.Severity < model.Crit) {
				out = append(out, hwChange(changeNew, f, p, had))
			}
		case !had || p.Severity < model.Warn:
			out = append(out, hwChange(changeNew, f, p, had))
		case p.Severity < f.Severity:
			out = append(out, hwChange(changeWorse, f, p, had))
		}
	}

	// Resolutions need a complete check that covered the component, run with
	// at least the privileges of the previous one.
	if !baseline || cur.Partial || (privileged(prev) && !privileged(cur)) {
		return out
	}
	for _, k := range prevOrder {
		p := prevF[k]
		if p.Severity != model.Crit {
			continue
		}
		if c, ok := curF[k]; ok && c.Severity >= model.Warn {
			continue
		}
		if !componentChecked(cur.Report, p.Component) {
			continue
		}
		ch := hwChange(changeResolved, p, p, true)
		ch.To = model.OK.String()
		if c, ok := curF[k]; ok {
			ch.To = c.Severity.String()
		}
		out = append(out, ch)
	}
	return out
}

func hwChange(kind string, f, prev model.Finding, had bool) health.HardwareChange {
	ch := health.HardwareChange{
		Change: kind, ID: f.ID, Target: f.Target, Component: f.Component,
		To: f.Severity.String(), Title: health.Text{EN: f.Title.EN, VI: f.Title.VI},
	}
	if had {
		ch.From = prev.Severity.String()
	}
	return ch
}

// verdictLevel maps a Diagward verdict onto the health levels alerts use.
func verdictLevel(res *HardwareResult) health.Level {
	if res == nil || res.Report == nil {
		return health.LevelUnknown
	}
	switch res.Report.Verdict {
	case model.Crit:
		return health.LevelCrit
	case model.Warn:
		return health.LevelWarn
	}
	return health.LevelOK
}

// hardwareAlerts turns the changes of one check into at most two alerts: one
// for new or worse problems and one for resolved critical problems.
func hardwareAlerts(hostID, hostName string, prev *HardwareResult, changes []health.HardwareChange) []health.Alert {
	var problems, resolved []health.HardwareChange
	for _, c := range changes {
		if c.Change == changeResolved {
			resolved = append(resolved, c)
		} else {
			problems = append(problems, c)
		}
	}
	var out []health.Alert
	if len(problems) > 0 {
		to := health.LevelWarn
		for _, c := range problems {
			if c.To == model.Crit.String() {
				to = health.LevelCrit
				break
			}
		}
		title := health.Text{
			EN: fmt.Sprintf("%s has a hardware warning", hostName),
			VI: fmt.Sprintf("%s có cảnh báo phần cứng", hostName),
		}
		if to == health.LevelCrit {
			title = health.Text{
				EN: fmt.Sprintf("%s has a critical hardware problem", hostName),
				VI: fmt.Sprintf("%s có lỗi phần cứng nghiêm trọng", hostName),
			}
		}
		// Most severe first, so the body names the worst change.
		ordered := append([]health.HardwareChange{}, problems...)
		for i := 1; i < len(ordered); i++ {
			for j := i; j > 0 && ordered[j].To == model.Crit.String() && ordered[j-1].To != model.Crit.String(); j-- {
				ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
			}
		}
		body := changeBody(ordered)
		out = append(out, health.Alert{
			HostID: hostID, HostName: hostName, From: verdictLevel(prev), To: to,
			Kind: "hardware", Title: &title, Body: &body, Hardware: ordered,
		})
	}
	if len(resolved) > 0 {
		title := health.Text{
			EN: fmt.Sprintf("%s: hardware problem resolved", hostName),
			VI: fmt.Sprintf("%s: lỗi phần cứng đã được khắc phục", hostName),
		}
		if len(resolved) > 1 {
			title = health.Text{
				EN: fmt.Sprintf("%s: %d hardware problems resolved", hostName, len(resolved)),
				VI: fmt.Sprintf("%s: %d lỗi phần cứng đã được khắc phục", hostName, len(resolved)),
			}
		}
		body := changeBody(resolved)
		// A recovery: To is OK even if other, lesser problems remain (the
		// title says "resolved", not "healthy").
		out = append(out, health.Alert{
			HostID: hostID, HostName: hostName, From: health.LevelCrit, To: health.LevelOK,
			Kind: "hardware", Title: &title, Body: &body, Hardware: resolved,
		})
	}
	return out
}

// changeBody is the first change's title (with its target when the title
// does not name it) plus how many more there are.
func changeBody(cs []health.HardwareChange) health.Text {
	line := func(c health.HardwareChange, title string) string {
		if c.Target != "" && !strings.Contains(title, c.Target) {
			title += " (" + c.Target + ")"
		}
		return title
	}
	first := cs[0]
	b := health.Text{EN: line(first, first.Title.In("en")), VI: line(first, first.Title.In("vi"))}
	if n := len(cs) - 1; n > 0 {
		b.EN += fmt.Sprintf(" · %d more", n)
		b.VI += fmt.Sprintf(" · và %d mục khác", n)
	}
	return b
}
