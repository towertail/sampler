package collect

import (
	"runtime"

	"github.com/shirou/gopsutil/v4/host"
	"github.com/towertail/sampler/internal/version"
	"github.com/towertail/sampler/pkg/schema"
)

func Host() (schema.HostInfo, []string) {
	var errs []string
	h := schema.HostInfo{
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Sampler: version.String(),
	}
	info, err := host.Info()
	if err != nil {
		errs = append(errs, "host.Info: "+err.Error())
		return h, errs
	}
	h.Name = info.Hostname
	h.Kernel = info.KernelVersion
	h.UptimeS = int64(info.Uptime)
	// HostID is /etc/machine-id on Linux, IOPlatformUUID on Darwin.
	// Read-only; omitempty when the host doesn't expose one (containers, etc).
	h.MachineID = stableMachineID(info.HostID)
	return h, errs
}

// MachineID returns the same id as host.machine_id in a sample. It is
// empty when the host has no id that survives a reboot.
func MachineID() string {
	id, err := host.HostID()
	if err != nil {
		return ""
	}
	return stableMachineID(id)
}
