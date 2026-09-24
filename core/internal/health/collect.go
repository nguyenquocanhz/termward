// Package health checks servers without installing an agent: one POSIX sh
// script is piped over the pooled SSH connection, and its key=value output is
// parsed into a Sample.
package health

import (
	"bufio"
	_ "embed"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed collect.sh
var rawScript string

// Script is the collector with CRLF stripped, in case a Windows checkout
// converted line endings — sh would choke on them.
var Script = strings.ReplaceAll(rawScript, "\r", "")

type Disk struct {
	Mount   string  `json:"mount"`
	TotalKB uint64  `json:"totalKb"`
	UsedKB  uint64  `json:"usedKb"`
	AvailKB uint64  `json:"availKb"`
	Percent float64 `json:"percent"`
}

type Container struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Status string `json:"status"`
	Health string `json:"health,omitempty"` // healthy | unhealthy | starting
}

type Sample struct {
	At          time.Time   `json:"at"`
	LatencyMs   int64       `json:"latencyMs"`
	OS          string      `json:"os"`
	Kernel      string      `json:"kernel"`
	Hostname    string      `json:"hostname"`
	CPUs        int         `json:"cpus"`
	UptimeSec   float64     `json:"uptimeSec"`
	Load        [3]float64  `json:"load"`
	CPUPercent  float64     `json:"cpuPercent"` // -1 when unknown
	MemTotalKB  uint64      `json:"memTotalKb"`
	MemUsedKB   uint64      `json:"memUsedKb"`
	MemPercent  float64     `json:"memPercent"` // -1 when unknown
	SwapTotalKB uint64      `json:"swapTotalKb"`
	SwapUsedKB  uint64      `json:"swapUsedKb"`
	Disks       []Disk      `json:"disks"`
	Systemd     bool        `json:"systemd"`
	FailedUnits []string    `json:"failedUnits"`
	Docker      string      `json:"docker"` // "" (not installed) | ok | denied
	Containers  []Container `json:"containers"`
	Reboot      bool        `json:"rebootRequired"`
}

// Parse turns collector output into a Sample. Unknown lines (for example a
// noisy .bashrc) are ignored.
func Parse(out string) Sample {
	s := Sample{CPUPercent: -1, MemPercent: -1, Disks: []Disk{}, FailedUnits: []string{}, Containers: []Container{}}
	mem := map[string]uint64{}
	var cpu0, cpu1 []uint64
	seenMount := map[string]bool{}

	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch {
		case key == "os":
			s.OS = strings.Trim(val, `"`)
		case key == "kernel":
			s.Kernel = val
		case key == "hostname":
			s.Hostname = val
		case key == "nproc":
			s.CPUs, _ = strconv.Atoi(val)
		case key == "uptime":
			s.UptimeSec, _ = strconv.ParseFloat(val, 64)
		case key == "load":
			for i, f := range strings.Fields(val) {
				if i < 3 {
					s.Load[i], _ = strconv.ParseFloat(f, 64)
				}
			}
		case key == "cpu0":
			cpu0 = cpuFields(val)
		case key == "cpu1":
			cpu1 = cpuFields(val)
		case strings.HasPrefix(key, "mem."):
			n, _ := strconv.ParseUint(val, 10, 64)
			mem[key[4:]] = n
		case key == "disk":
			if d, ok := parseDisk(val); ok && !seenMount[d.Mount] {
				seenMount[d.Mount] = true
				s.Disks = append(s.Disks, d)
			}
		case key == "systemd":
			s.Systemd = true
		case key == "failed":
			s.FailedUnits = append(s.FailedUnits, val)
		case key == "docker":
			s.Docker = val
		case key == "ctr":
			if c, ok := parseContainer(val); ok {
				s.Containers = append(s.Containers, c)
			}
		case key == "reboot":
			s.Reboot = val == "1"
		}
	}

	s.CPUPercent = cpuPercent(cpu0, cpu1)

	if total := mem["MemTotal"]; total > 0 {
		avail, ok := mem["MemAvailable"]
		if !ok { // kernels older than 3.14
			avail = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
		}
		avail = min(avail, total)
		s.MemTotalKB = total
		s.MemUsedKB = total - avail
		s.MemPercent = round1(float64(s.MemUsedKB) / float64(total) * 100)
	}
	if st := mem["SwapTotal"]; st > 0 {
		s.SwapTotalKB = st
		s.SwapUsedKB = st - min(mem["SwapFree"], st)
	}
	sort.Slice(s.Disks, func(i, j int) bool { return s.Disks[i].Mount < s.Disks[j].Mount })
	return s
}

func cpuFields(line string) []uint64 {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return nil
	}
	out := make([]uint64, 0, 8)
	for _, x := range f[1:min(len(f), 9)] { // user..steal; guest is already in user
		n, _ := strconv.ParseUint(x, 10, 64)
		out = append(out, n)
	}
	return out
}

func cpuPercent(a, b []uint64) float64 {
	if len(a) < 4 || len(a) != len(b) {
		return -1
	}
	var ta, tb uint64
	for i := range a {
		ta += a[i]
		tb += b[i]
	}
	idleA, idleB := a[3], b[3]
	if len(a) > 4 { // iowait counts as idle
		idleA += a[4]
		idleB += b[4]
	}
	if tb <= ta {
		return -1
	}
	busy := float64((tb-ta)-(idleB-idleA)) / float64(tb-ta) * 100
	return round1(min(max(busy, 0), 100))
}

func parseDisk(v string) (Disk, bool) {
	p := strings.Split(v, "|")
	if len(p) != 4 {
		return Disk{}, false
	}
	total, err1 := strconv.ParseUint(p[1], 10, 64)
	used, err2 := strconv.ParseUint(p[2], 10, 64)
	avail, err3 := strconv.ParseUint(p[3], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || total == 0 {
		return Disk{}, false
	}
	d := Disk{Mount: p[0], TotalKB: total, UsedKB: used, AvailKB: avail}
	if used+avail > 0 { // same formula as df's Use% (excludes root-reserved blocks)
		d.Percent = round1(float64(used) / float64(used+avail) * 100)
	}
	return d, true
}

func parseContainer(v string) (Container, bool) {
	p := strings.SplitN(v, "|", 3)
	if len(p) != 3 || p[0] == "" {
		return Container{}, false
	}
	c := Container{Name: p[0], State: p[1], Status: p[2]}
	switch {
	case strings.Contains(p[2], "(unhealthy)"):
		c.Health = "unhealthy"
	case strings.Contains(p[2], "(healthy)"):
		c.Health = "healthy"
	case strings.Contains(p[2], "(health: starting)"):
		c.Health = "starting"
	}
	return c, true
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}
