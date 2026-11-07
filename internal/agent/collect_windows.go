package agent

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/sudatti624/AmuOS/internal/metrics"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	collectorKernel    = windows.NewLazySystemDLL("kernel32.dll")
	getSystemTimes     = collectorKernel.NewProc("GetSystemTimes")
	globalMemoryStatus = collectorKernel.NewProc("GlobalMemoryStatusEx")
	getUptime          = collectorKernel.NewProc("GetTickCount64")
)

// The layout is MEMORYSTATUSEX, including the reserved final field.
type memoryStatus struct {
	Length, Load                                       uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile uint64
	TotalVirtual, AvailVirtual, AvailExtendedVirtual   uint64
}

func readWindowsMemory() (memoryStatus, bool) {
	m := memoryStatus{}
	m.Length = uint32(unsafe.Sizeof(m))
	ok, _, _ := globalMemoryStatus.Call(uintptr(unsafe.Pointer(&m)))
	return m, ok != 0
}

type windowsCollector struct {
	cpu         cpuTimes
	io          counters
	disk        *diskQuery
	diskStarted bool
}

func NewCollector() Collector { return &windowsCollector{} }

func (c *windowsCollector) Sample(now time.Time) (metrics.Sample, error) {
	s := metrics.Sample{CollectedAt: now.Unix()}
	// GetSystemTimes covers only one processor group on large machines.
	if windows.GetActiveProcessorCount(0xffff) <= 64 {
		var idle, kernel, user windows.Filetime
		ok, _, _ := getSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
		if ok != 0 {
			value := func(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
			total, idleTicks := value(kernel)+value(user), value(idle)
			if idleTicks <= total {
				current := cpuTimes{busy: total - idleTicks, total: total}
				if c.cpu.total > 0 {
					s.CPU = cpuPercent(c.cpu, current)
				}
				c.cpu = current
			}
		} else {
			c.cpu = cpuTimes{}
		}
	}
	if m, ok := readWindowsMemory(); ok {
		s.Memory = usedPercent(m.TotalPhys, m.AvailPhys, true)
	}
	millis, _, _ := getUptime.Call()
	up := int64(millis / 1000)
	s.Uptime = &up
	if !c.diskStarted {
		c.disk = newDiskQuery()
		c.diskStarted = true
	}
	s.DiskRead, s.DiskWrite = c.disk.sample()
	if total, free, ok := windowsDisk(systemDrive()); ok {
		s.Disk = usedPercent(total, free, true)
	}
	values := map[string]uint64{}
	if interfaces, err := net.Interfaces(); err == nil {
		for _, iface := range interfaces {
			row := windows.MibIfRow2{InterfaceIndex: uint32(iface.Index)}
			if windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &row) != nil || row.InterfaceAndOperStatusFlags&1 == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			key := fmt.Sprint(row.InterfaceLuid)
			values[key+"rx"], values[key+"tx"] = row.InOctets, row.OutOctets
		}
	}
	rates := c.io.rates(now, values)
	for key, rate := range rates {
		if rate != nil {
			if strings.HasSuffix(key, "rx") {
				if s.NetRx == nil {
					s.NetRx = new(float64)
				}
				*s.NetRx += *rate
			} else {
				if s.NetTx == nil {
					s.NetTx = new(float64)
				}
				*s.NetTx += *rate
			}
		}
	}
	// Windows commit limits are not Linux swap capacity. Leave swap and
	// Unix load averages unavailable rather than changing their meaning.
	return s, nil
}

func (c *windowsCollector) HostInfo(now time.Time) (metrics.HostInfo, error) {
	info := metrics.HostInfo{CollectedAt: now.Unix(), OS: "Windows", Arch: runtime.GOARCH, Filesystems: []metrics.Filesystem{}, Interfaces: []metrics.Interface{}}
	if name, err := os.Hostname(); err == nil {
		info.Hostname = clean(name)
	}
	v := windows.RtlGetVersion()
	info.Kernel = fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
	if count := int(windows.GetActiveProcessorCount(0xffff)); count > 0 {
		info.CPUCount = &count
	}
	if m, ok := readWindowsMemory(); ok && m.TotalPhys > 0 {
		info.MemoryBytes = &m.TotalPhys
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		model, _, _ := k.GetStringValue("ProcessorNameString")
		info.CPUModel = clean(model)
		k.Close()
	}
	var drives [512]uint16
	if n, err := windows.GetLogicalDriveStrings(uint32(len(drives)), &drives[0]); err == nil && n < uint32(len(drives)) {
		// UTF16ToString stops at the first NUL; iterate the MULTI_SZ directly.
		start := 0
		for i := 0; i < int(n) && len(info.Filesystems) < 64; i++ {
			if drives[i] != 0 {
				continue
			}
			if i == start {
				break
			}
			drive := windows.UTF16ToString(drives[start:i])
			start = i + 1
			root, _ := windows.UTF16PtrFromString(drive)
			if windows.GetDriveType(root) != windows.DRIVE_FIXED {
				continue
			}
			f := metrics.Filesystem{Mount: drive}
			var fs [64]uint16
			if windows.GetVolumeInformation(root, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))) == nil {
				f.Type = clean(windows.UTF16ToString(fs[:]))
			}
			if total, free, ok := windowsDisk(drive); ok {
				used := total - free
				f.TotalBytes, f.UsedBytes = &total, &used
			}
			info.Filesystems = append(info.Filesystems, f)
		}
	}
	if interfaces, err := net.Interfaces(); err == nil {
		for _, iface := range interfaces {
			if iface.Flags&net.FlagLoopback != 0 || len(info.Interfaces) >= 64 {
				continue
			}
			entry := metrics.Interface{Name: clean(iface.Name), Addresses: []string{}}
			if addrs, err := iface.Addrs(); err == nil {
				for _, addr := range addrs {
					if len(entry.Addresses) == 16 {
						break
					}
					entry.Addresses = append(entry.Addresses, clean(addr.String()))
				}
			}
			if entry.Name != "" {
				info.Interfaces = append(info.Interfaces, entry)
			}
		}
	}
	return info, nil
}

func systemDrive() string {
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	return drive + `\`
}
func windowsDisk(path string) (uint64, uint64, bool) {
	root, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(root, &available, &total, &free)
	return total, free, err == nil && total > 0 && free <= total
}
