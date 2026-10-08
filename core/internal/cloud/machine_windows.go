//go:build windows

package cloud

import "golang.org/x/sys/windows/registry"

// systemMachineID reads HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid
// (from the 64-bit view, also when running as a 32-bit process).
func systemMachineID() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", errNoMachineID
	}
	return v, nil
}
