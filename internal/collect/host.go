package collect

import (
	"os"
	"runtime"
	"sync"

	"github.com/shirou/gopsutil/v4/host"
	"github.com/towertail/sampler/internal/version"
	"github.com/towertail/sampler/pkg/schema"
)

// Host reads each field on its own, so one failed field does not drop the
// others. host.Info() does more work than the sampler needs.
func Host() (schema.HostInfo, []string) {
	var errs []string
	h := schema.HostInfo{
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Sampler: version.String(),
	}
	var err error
	if h.Name, err = os.Hostname(); err != nil {
		errs = append(errs, "host.name: "+err.Error())
	}
	if h.Kernel, err = host.KernelVersion(); err != nil {
		errs = append(errs, "host.kernel: "+err.Error())
	}
	if up, err := host.Uptime(); err != nil {
		errs = append(errs, "host.uptime: "+err.Error())
	} else {
		h.UptimeS = int64(up)
	}
	// HostID is /etc/machine-id on Linux, IOPlatformUUID on Darwin.
	// Empty when the host has none (containers, etc).
	if id, err := hostIDs.get(); err != nil {
		errs = append(errs, "host.id: "+err.Error())
	} else {
		h.MachineID = stableMachineID(id)
	}
	return h, errs
}

// MachineID returns the same id as host.machine_id in a sample. It is
// empty when the host has no id that survives a reboot.
func MachineID() string {
	id, err := hostIDs.get()
	if err != nil {
		return ""
	}
	return stableMachineID(id)
}

// hostIDCache keeps the first good host ID. On macOS each read starts
// `ioreg`.
type hostIDCache struct {
	mu   sync.Mutex
	id   string
	done bool
}

var hostIDs hostIDCache

func (c *hostIDCache) get() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return c.id, nil
	}
	id, err := host.HostID()
	if err != nil {
		return "", err
	}
	c.id, c.done = id, true
	return id, nil
}
