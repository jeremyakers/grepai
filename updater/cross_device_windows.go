//go:build windows

package updater

func isCrossDeviceError(err error) bool {
	return false
}
