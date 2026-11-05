package agent

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/sudatti624/AmuOS/internal/metrics"
)

// linuxCollector reads /proc and /sys only; it needs no privileges.
type linuxCollector struct {
	cpu cpuTimes
	io  counters
}

func NewCollector() Collector { return &linuxCollector{} }

func (c *linuxCollector) read(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// wholeDisk counts each physical or virtual disk once: partitions, loop and
// RAM devices, and device-mapper or RAID layers over other disks are skipped.
func wholeDisk(name string) bool {
	for _, prefix := range []string{"loop", "ram", "zram", "dm-", "md", "sr", "fd"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return exists(filepath.Join("/sys/block", name))
}

// physicalInterface skips loopback, bridges, veth pairs and other virtual
// interfaces, which would count the same traffic again.
func physicalInterface(name string) bool {
	return name != "lo" && exists(filepath.Join("/sys/class/net", name, "device"))
}

func statfs(path string) (total, used, avail uint64, ok bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil || st.Blocks == 0 {
		return 0, 0, 0, false
	}
	size := uint64(st.Bsize)
	return st.Blocks * size, (st.Blocks - st.Bfree) * size, st.Bavail * size, true
}

func (c *linuxCollector) Sample(now time.Time) (metrics.Sample, error) {
	s := metrics.Sample{CollectedAt: now.Unix()}
	if times, _, err := parseStat(c.read("/proc/stat")); err == nil {
		if c.cpu.total > 0 {
			s.CPU = cpuPercent(c.cpu, times)
		}
		c.cpu = times
	}
	s.Load1, s.Load5, s.Load15 = parseLoadavg(c.read("/proc/loadavg"))
	mem := parseMeminfo(c.read("/proc/meminfo"))
	total, okTotal := mem["MemTotal"]
	available, okAvailable := mem["MemAvailable"]
	s.Memory = usedPercent(total, available, okTotal && okAvailable)
	swapTotal, okSwap := mem["SwapTotal"]
	swapFree, okFree := mem["SwapFree"]
	s.Swap = usedPercent(swapTotal, swapFree, okSwap && okFree)
	if _, used, avail, ok := statfs("/"); ok && used+avail > 0 {
		// Like df: reserved blocks count as neither used nor available.
		v := float64(used) / float64(used+avail) * 100
		s.Disk = &v
	}
	values := map[string]uint64{}
	if read, written, ok := parseDiskstats(c.read("/proc/diskstats"), wholeDisk); ok {
		values["disk_read"], values["disk_write"] = read, written
	}
	if rx, tx, ok := parseNetDev(c.read("/proc/net/dev"), physicalInterface); ok {
		values["net_rx"], values["net_tx"] = rx, tx
	}
	rates := c.io.rates(now, values)
	s.DiskRead, s.DiskWrite, s.NetRx, s.NetTx = rates["disk_read"], rates["disk_write"], rates["net_rx"], rates["net_tx"]
	if fields := strings.Fields(c.read("/proc/uptime")); len(fields) > 0 {
		if v, err := strconv.ParseFloat(fields[0], 64); err == nil && v >= 0 {
			up := int64(v)
			s.Uptime = &up
		}
	}
	return s, nil
}

func (c *linuxCollector) HostInfo(now time.Time) (metrics.HostInfo, error) {
	info := metrics.HostInfo{CollectedAt: now.Unix(), Arch: runtime.GOARCH, Filesystems: []metrics.Filesystem{}, Interfaces: []metrics.Interface{}}
	if name, err := os.Hostname(); err == nil {
		info.Hostname = clean(name)
	}
	info.OS = clean(parseOSRelease(c.read("/etc/os-release")))
	info.Kernel = clean(c.read("/proc/sys/kernel/osrelease"))
	model, count := parseCPUInfo(c.read("/proc/cpuinfo"))
	info.CPUModel = clean(model)
	if count > 0 {
		info.CPUCount = &count
	}
	mem := parseMeminfo(c.read("/proc/meminfo"))
	if v, ok := mem["MemTotal"]; ok && v > 0 {
		info.MemoryBytes = &v
	}
	if v, ok := mem["SwapTotal"]; ok {
		info.SwapBytes = &v
	}
	_, info.BootTime, _ = parseStat(c.read("/proc/stat"))
	for _, m := range parseMounts(c.read("/proc/self/mounts")) {
		if len(info.Filesystems) == 64 {
			break
		}
		f := metrics.Filesystem{Mount: clean(m.path), Type: clean(m.fstype)}
		if f.Mount == "" {
			continue
		}
		if total, used, _, ok := statfs(m.path); ok {
			f.TotalBytes, f.UsedBytes = &total, &used
		}
		info.Filesystems = append(info.Filesystems, f)
	}
	if interfaces, err := net.Interfaces(); err == nil {
		for _, i := range interfaces {
			if len(info.Interfaces) == 64 {
				break
			}
			if i.Flags&net.FlagLoopback != 0 {
				continue
			}
			entry := metrics.Interface{Name: clean(i.Name), Addresses: []string{}}
			if addrs, err := i.Addrs(); err == nil {
				for _, a := range addrs {
					if len(entry.Addresses) == 16 {
						break
					}
					entry.Addresses = append(entry.Addresses, clean(a.String()))
				}
			}
			if entry.Name != "" {
				info.Interfaces = append(info.Interfaces, entry)
			}
		}
	}
	return info, nil
}
