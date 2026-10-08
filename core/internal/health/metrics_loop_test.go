package health

import (
	"bufio"
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMetricsLoopFormatUnderShells runs the real loop script against this
// machine's /proc under both a POSIX sh (dash on Ubuntu) and busybox sh via
// WSL, and asserts it prints one well-formed TWM line. It is skipped anywhere
// WSL or busybox is unavailable (CI, non-Windows), so it never blocks the
// normal `go test` run.
func TestMetricsLoopFormatUnderShells(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("WSL loop-format check runs on the Windows dev machine only")
	}
	if _, err := exec.LookPath("wsl"); err != nil {
		t.Skip("wsl not found")
	}
	for _, shell := range [][]string{{"sh", "-s", "1"}, {"busybox", "sh", "-s", "1"}} {
		t.Run(shell[0], func(t *testing.T) {
			line, err := firstTWMLine(t, shell)
			if err != nil {
				t.Skipf("%v not runnable under WSL: %v", shell, err)
			}
			s, ok := parseTWM(line)
			if !ok {
				t.Fatalf("loop produced a malformed line: %q", line)
			}
			if s.memTotal == 0 || s.ncpu < 1 {
				t.Fatalf("implausible /proc values: %q -> %+v", line, s)
			}
			if s.at == 0 {
				t.Errorf("epoch not set: %q", line)
			}
		})
	}
}

// firstTWMLine runs `wsl -e <shell...>` with the loop script on stdin, reads
// the first "TWM " line, then stops the process.
func firstTWMLine(t *testing.T, shell []string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	args := append([]string{"-e"}, shell...)
	cmd := exec.CommandContext(ctx, "wsl", args...)
	cmd.Stdin = strings.NewReader(MetricsScript)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if line := sc.Text(); len(line) >= 4 && line[:4] == "TWM " {
			return line, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", context.DeadlineExceeded
}
