package collect

import (
	"bytes"
	"os"
	"strconv"
	"strings"

	"github.com/towertail/sampler/pkg/schema"
)

func healthPlatform(h *schema.HealthInfo) []string {
	var errs []string

	entries, err := scanProcStat("/proc")
	if err != nil {
		errs = append(errs, "health.procs: "+err.Error())
	} else {
		applyProcEntries(h, entries)
	}

	// /proc/loadavg field 4 is "running/total" scheduling entities
	// (threads included). pid_max and threads-max both cap that total.
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 4 {
			if _, total, ok := strings.Cut(f[3], "/"); ok {
				if n, err := strconv.ParseInt(total, 10, 64); err == nil {
					h.PidsUsed = &n
				}
			}
		}
	}
	pidMax, err1 := readInt("/proc/sys/kernel/pid_max")
	thrMax, err2 := readInt("/proc/sys/kernel/threads-max")
	switch {
	case err1 == nil && err2 == nil:
		m := min(pidMax, thrMax)
		h.PidsMax = &m
	case err1 == nil:
		h.PidsMax = &pidMax
	}

	// file-nr: allocated, free (always 0 since 2.6), max.
	if b, err := os.ReadFile("/proc/sys/fs/file-nr"); err == nil {
		if f := strings.Fields(string(b)); len(f) == 3 {
			used, e1 := strconv.ParseInt(f[0], 10, 64)
			max, e2 := strconv.ParseInt(f[2], 10, 64)
			if e1 == nil && e2 == nil {
				h.FilesUsed = &used
				h.FilesMax = &max
			}
		}
	}

	h.PSI = readPSI("/proc/pressure")
	return errs
}

// scanProcStat reads pid, comm, state, and ppid from every
// /proc/<pid>/stat. It is much cheaper than a full gopsutil process
// build: one small read per process and no other files.
func scanProcStat(root string) ([]procEntry, error) {
	dir, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make([]procEntry, 0, len(dir))
	buf := make([]byte, 512)
	for _, d := range dir {
		pid, err := strconv.ParseInt(d.Name(), 10, 32)
		if err != nil {
			continue
		}
		f, err := os.Open(root + "/" + d.Name() + "/stat")
		if err != nil {
			continue
		}
		n, _ := f.Read(buf)
		f.Close()
		if e, ok := parseProcStat(buf[:n]); ok {
			e.pid = int32(pid)
			out = append(out, e)
		}
	}
	return out, nil
}

// parseProcStat parses "pid (comm) S ppid ...". comm can contain
// spaces and parens, so split on the last ')'.
func parseProcStat(b []byte) (procEntry, bool) {
	open := bytes.IndexByte(b, '(')
	close := bytes.LastIndexByte(b, ')')
	if open < 0 || close < open || close+2 >= len(b) {
		return procEntry{}, false
	}
	rest := strings.Fields(string(b[close+2:]))
	if len(rest) < 2 {
		return procEntry{}, false
	}
	ppid, err := strconv.ParseInt(rest[1], 10, 32)
	if err != nil {
		return procEntry{}, false
	}
	return procEntry{
		ppid:   int32(ppid),
		name:   string(b[open+1 : close]),
		zombie: rest[0] == "Z",
	}, true
}

func readInt(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// readPSI returns avg10 values from /proc/pressure, or nil when PSI is
// not available (kernel < 4.20 or psi=0 on the boot line).
func readPSI(dir string) *schema.PSIInfo {
	cpu, err := os.ReadFile(dir + "/cpu")
	if err != nil {
		return nil
	}
	mem, _ := os.ReadFile(dir + "/memory")
	io, _ := os.ReadFile(dir + "/io")
	var p schema.PSIInfo
	p.CPUSome, _ = psiAvg10(cpu)
	p.MemSome, p.MemFull = psiAvg10(mem)
	p.IOSome, p.IOFull = psiAvg10(io)
	return &p
}

// psiAvg10 parses the avg10 value of the "some" and "full" lines.
func psiAvg10(b []byte) (some, full float64) {
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasPrefix(f[1], "avg10=") {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimPrefix(f[1], "avg10="), 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "some":
			some = v
		case "full":
			full = v
		}
	}
	return
}
