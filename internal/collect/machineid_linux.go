package collect

import (
	"os"
	"strings"
)

// stableMachineID drops an id that does not survive a reboot. gopsutil
// falls back to the boot id when the host has no product_uuid that the
// user can read and no /etc/machine-id.
func stableMachineID(hostID string) string {
	return stableID(hostID, "/sys/class/dmi/id/product_uuid", "/etc/machine-id")
}

func stableID(hostID, productUUID, machineID string) string {
	if b, err := os.ReadFile(productUUID); err == nil && strings.TrimSpace(string(b)) != "" {
		return hostID
	}
	if _, err := os.Stat(machineID); err == nil {
		return hostID
	}
	return ""
}
