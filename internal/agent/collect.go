package agent

import (
	"bufio"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/sudatti624/AmuOS/internal/metrics"
)

// ErrUnsupported means this platform has no collector; the Agent then only
// sends heartbeats.
var ErrUnsupported = errors.New("metrics collection is not supported on this platform")

// Collector reads the host. Sample leaves Seq for the Agent to assign.
type Collector interface {
	HostInfo(now time.Time) (metrics.HostInfo, error)
	Sample(now time.Time) (metrics.Sample, error)
}

// counters turns cumulative counters into rates. A first reading, a counter
// that went backwards (reboot, reset, wrap) or a non-positive interval gives
// nil rather than a made-up value.
type counters struct {
	at     time.Time
	values map[string]uint64
}

func (c *counters) rates(now time.Time, values map[string]uint64) map[string]*float64 {
	rates := map[string]*float64{}
	elapsed := now.Sub(c.at).Seconds()
	for name, value := range values {
		previous, ok := c.values[name]
		if !ok || c.at.IsZero() || elapsed <= 0 || value < previous {
			rates[name] = nil
			continue
		}
		rate := float64(value-previous) / elapsed
		rates[name] = &rate
	}
	c.at, c.values = now, values
	return rates
}

type cpuTimes struct{ busy, total uint64 }

// parseStat reads the aggregate "cpu" line and btime from /proc/stat.
func parseStat(data string) (cpuTimes, *int64, error) {
	var times cpuTimes
	var boot *int64
	found := false
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "cpu":
			if len(fields) < 5 {
				return times, nil, errors.New("short cpu line")
			}
			// user nice system idle iowait irq softirq steal; guest time is
			// already counted in user and nice.
			for i, f := range fields[1:min(len(fields), 9)] {
				v, err := strconv.ParseUint(f, 10, 64)
				if err != nil {
					return times, nil, err
				}
				times.total += v
				if i != 3 && i != 4 {
					times.busy += v
				}
			}
			found = true
		case "btime":
			if len(fields) == 2 {
				if v, err := strconv.ParseInt(fields[1], 10, 64); err == nil && v > 0 {
					boot = &v
				}
			}
		}
	}
	if !found {
		return times, nil, errors.New("no cpu line")
	}
	return times, boot, scanner.Err()
}

func cpuPercent(previous, current cpuTimes) *float64 {
	if current.total <= previous.total || current.busy < previous.busy {
		return nil
	}
	v := float64(current.busy-previous.busy) / float64(current.total-previous.total) * 100
	return &v
}

// parseMeminfo returns values in bytes.
func parseMeminfo(data string) map[string]uint64 {
	values := map[string]uint64{}
	for _, line := range strings.Split(data, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 1 && fields[1] == "kB" {
			v *= 1024
		}
		values[name] = v
	}
	return values
}

func usedPercent(total, available uint64, ok bool) *float64 {
	if !ok || total == 0 || available > total {
		return nil
	}
	v := float64(total-available) / float64(total) * 100
	return &v
}

func parseLoadavg(data string) (l1, l5, l15 *float64) {
	fields := strings.Fields(data)
	if len(fields) < 3 {
		return nil, nil, nil
	}
	values := make([]*float64, 3)
	for i := range values {
		if v, err := strconv.ParseFloat(fields[i], 64); err == nil && v >= 0 {
			values[i] = &v
		}
	}
	return values[0], values[1], values[2]
}

// parseDiskstats sums sectors read and written (always 512 bytes) for the
// devices include accepts, so partitions are not counted twice.
func parseDiskstats(data string, include func(string) bool) (read, written uint64, ok bool) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || !include(fields[2]) {
			continue
		}
		r, err1 := strconv.ParseUint(fields[5], 10, 64)
		w, err2 := strconv.ParseUint(fields[9], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		read, written, ok = read+r*512, written+w*512, true
	}
	return read, written, ok
}

// parseNetDev sums received and transmitted bytes for the accepted interfaces.
func parseNetDev(data string, include func(string) bool) (rx, tx uint64, ok bool) {
	for _, line := range strings.Split(data, "\n") {
		name, rest, found := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !found || !include(name) {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		r, err1 := strconv.ParseUint(fields[0], 10, 64)
		t, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rx, tx, ok = rx+r, tx+t, true
	}
	return rx, tx, ok
}

type mount struct{ device, path, fstype string }

// parseMounts keeps block-device and ZFS file systems, one entry per device.
func parseMounts(data string) []mount {
	var mounts []mount
	seen := map[string]bool{}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		m := mount{unescapeMount(fields[0]), unescapeMount(fields[1]), fields[2]}
		if (!strings.HasPrefix(m.device, "/dev/") && m.fstype != "zfs") || m.fstype == "squashfs" || seen[m.device] {
			continue
		}
		seen[m.device] = true
		mounts = append(mounts, m)
	}
	return mounts
}

// unescapeMount decodes the octal escapes (\040 for a space) in /proc/mounts.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseOSRelease returns PRETTY_NAME, or NAME and VERSION as a fallback.
func parseOSRelease(data string) string {
	values := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, `'"`)
		}
		values[key] = value
	}
	if values["PRETTY_NAME"] != "" {
		return values["PRETTY_NAME"]
	}
	return strings.TrimSpace(values["NAME"] + " " + values["VERSION"])
}

func parseCPUInfo(data string) (model string, count int) {
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "processor":
			count++
		case "model name":
			if model == "" {
				model = strings.TrimSpace(value)
			}
		}
	}
	return model, count
}

// clean keeps text within the Control Plane's limits.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > 256 {
		// Cutting may split a character; ToValidUTF8 drops the remainder.
		s = s[:256]
	}
	return strings.ToValidUTF8(s, "")
}
