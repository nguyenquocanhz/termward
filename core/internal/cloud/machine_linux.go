//go:build linux

package cloud

import "os"

// systemMachineID reads the systemd/dbus machine ID. On Android (which also
// builds with the linux tag) the files do not exist and the app passes
// ANDROID_ID instead.
func systemMachineID() (string, error) {
	return readFirstFile(os.ReadFile, "/etc/machine-id", "/var/lib/dbus/machine-id")
}
