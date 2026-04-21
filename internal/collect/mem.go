package collect

import (
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/towertail/sampler/internal/schema"
)

func Mem() (schema.MemInfo, schema.MemInfo, []string) {
	var errs []string
	var m, s schema.MemInfo

	vm, err := mem.VirtualMemory()
	if err != nil {
		errs = append(errs, "mem.VirtualMemory: "+err.Error())
	} else {
		m.Total = int64(vm.Total)
		// "used" excludes cached/inactive — total - available matches
		// what a human calls "memory in use" (spec §4).
		if vm.Available <= vm.Total {
			m.Used = int64(vm.Total - vm.Available)
		} else {
			m.Used = int64(vm.Used)
		}
	}

	sw, err := mem.SwapMemory()
	if err != nil {
		errs = append(errs, "mem.SwapMemory: "+err.Error())
	} else {
		s.Total = int64(sw.Total)
		s.Used = int64(sw.Used)
	}

	return m, s, errs
}
