//go:build darwin && !ios

package cloud

import (
	"context"
	"os/exec"
	"time"
)

// systemMachineID reads IOPlatformUUID from the I/O Registry.
func systemMachineID() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", err
	}
	return parseIoreg(string(out))
}
