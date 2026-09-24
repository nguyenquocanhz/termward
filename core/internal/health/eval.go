package health

import (
	"github.com/nguyenquocanhz/termward/core/internal/store"
)

type Level string

const (
	LevelUnknown Level = "unknown"
	LevelOK      Level = "ok"
	LevelInfo    Level = "info" // only used on findings, never as a host level
	LevelWarn    Level = "warn"
	LevelCrit    Level = "crit"
	LevelDown    Level = "down"
)

func (l Level) rank() int {
	switch l {
	case LevelOK:
		return 0
	case LevelWarn:
		return 1
	case LevelCrit:
		return 2
	case LevelDown:
		return 3
	}
	return -1
}

// Finding is one reason a host is not perfectly healthy. The UI localizes it
// from Code + Subject + Value/Threshold, so the core stays language-neutral.
type Finding struct {
	Code      string  `json:"code"`
	Level     Level   `json:"level"`
	Subject   string  `json:"subject,omitempty"`
	Value     float64 `json:"value,omitempty"`
	Threshold float64 `json:"threshold,omitempty"`
}

// Evaluate grades a sample against the thresholds. Findings are sorted from
// most to least severe.
func Evaluate(s Sample, t store.Thresholds) (Level, []Finding) {
	var fs []Finding
	grade := func(code, subject string, v, warn, crit float64) {
		switch {
		case v >= crit:
			fs = append(fs, Finding{Code: code, Level: LevelCrit, Subject: subject, Value: v, Threshold: crit})
		case v >= warn:
			fs = append(fs, Finding{Code: code, Level: LevelWarn, Subject: subject, Value: v, Threshold: warn})
		}
	}

	if s.CPUPercent >= 0 {
		grade("cpu", "", s.CPUPercent, t.CPUWarn, t.CPUCrit)
	}
	if s.MemPercent >= 0 {
		grade("mem", "", s.MemPercent, t.MemWarn, t.MemCrit)
	}
	for _, d := range s.Disks {
		grade("disk", d.Mount, d.Percent, t.DiskWarn, t.DiskCrit)
	}
	if s.CPUs > 0 {
		perCore := round1(s.Load[1] / float64(s.CPUs)) // 5-minute load: ignores short bursts
		grade("load", "", perCore, t.LoadPerCoreWarn, t.LoadPerCoreCrit)
	}
	for _, u := range s.FailedUnits {
		fs = append(fs, Finding{Code: "unit_failed", Level: LevelWarn, Subject: u})
	}
	for _, c := range s.Containers {
		switch {
		case c.Health == "unhealthy":
			fs = append(fs, Finding{Code: "container_unhealthy", Level: LevelCrit, Subject: c.Name})
		case c.State == "restarting":
			fs = append(fs, Finding{Code: "container_restarting", Level: LevelCrit, Subject: c.Name})
		case c.State == "dead":
			fs = append(fs, Finding{Code: "container_dead", Level: LevelWarn, Subject: c.Name})
		}
	}
	if s.Reboot {
		fs = append(fs, Finding{Code: "reboot_required", Level: LevelInfo})
	}

	level := LevelOK
	for _, f := range fs {
		if f.Level.rank() > level.rank() {
			level = f.Level
		}
	}
	sortFindings(fs)
	if fs == nil {
		fs = []Finding{}
	}
	return level, fs
}

func sortFindings(fs []Finding) {
	sev := func(l Level) int {
		if l == LevelInfo {
			return -1
		}
		return l.rank()
	}
	for i := 1; i < len(fs); i++ { // insertion sort keeps the stable order above
		for j := i; j > 0 && sev(fs[j].Level) > sev(fs[j-1].Level); j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}
