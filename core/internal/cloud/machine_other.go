//go:build !linux && !windows && !(darwin && !ios)

package cloud

// systemMachineID has no OS source here (iOS passes identifierForVendor from
// the app; other systems fall back to a generated ID).
func systemMachineID() (string, error) { return "", errNoMachineID }
