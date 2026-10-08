package cloud

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

// Where the machine ID of a fingerprint came from.
const (
	// MachineNative: passed in by the Android/iOS app (ANDROID_ID,
	// identifierForVendor).
	MachineNative = "native"
	// MachineSystem: read from the OS (Windows MachineGuid, Linux
	// /etc/machine-id, macOS IOPlatformUUID).
	MachineSystem = "system"
	// MachineGenerated: the OS gave nothing usable, so a random ID was created
	// once and kept in the secret store. It is stable for this install but,
	// unlike a system ID, does not survive a reinstall or a wiped keychain.
	MachineGenerated = "generated"
)

const secretMachineID = "cloud/machine-id"

var errNoMachineID = errors.New("machine id not available")

// resolveMachineID picks the machine ID: the one passed in by the native app,
// else the OS one, else a random one kept in the secret store.
func resolveMachineID(native string, system func() (string, error), sec Secrets) (id, source string, err error) {
	if v := strings.TrimSpace(native); v != "" {
		return v, MachineNative, nil
	}
	if system != nil {
		if v, err := system(); err == nil {
			if v = strings.TrimSpace(v); v != "" {
				return v, MachineSystem, nil
			}
		}
	}
	if v, err := sec.Get(secretMachineID); err == nil && v != "" {
		return v, MachineGenerated, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	v := hex.EncodeToString(b)
	if err := sec.Put(secretMachineID, v, true); err != nil {
		sec.Forget(secretMachineID)
		return "", "", secretStoreError(err)
	}
	return v, MachineGenerated, nil
}

// readFirstFile returns the trimmed content of the first non-empty file
// (Linux: /etc/machine-id, then /var/lib/dbus/machine-id).
func readFirstFile(read func(string) ([]byte, error), paths ...string) (string, error) {
	for _, p := range paths {
		b, err := read(p)
		if err != nil {
			continue
		}
		if v := strings.TrimSpace(string(b)); v != "" {
			return v, nil
		}
	}
	return "", errNoMachineID
}

var ioregUUID = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

// parseIoreg extracts IOPlatformUUID from `ioreg -rd1 -c IOPlatformExpertDevice`.
func parseIoreg(out string) (string, error) {
	m := ioregUUID.FindStringSubmatch(out)
	if m == nil || strings.TrimSpace(m[1]) == "" {
		return "", errNoMachineID
	}
	return strings.TrimSpace(m[1]), nil
}
