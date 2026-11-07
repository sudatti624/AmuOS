package agent

import (
	"testing"
	"time"
)

func TestWindowsCollectorNativeReadings(t *testing.T) {
	c := NewCollector()
	now := time.Now()
	h, err := c.HostInfo(now)
	if err != nil || h.Validate() != nil || h.OS != "Windows" || h.Hostname == "" || h.CPUCount == nil || h.MemoryBytes == nil || *h.MemoryBytes == 0 {
		t.Fatalf("host info: %+v, %v", h, err)
	}
	s, err := c.Sample(now)
	s.Seq = 1
	if err != nil || s.Validate() != nil || s.CPU != nil || s.Memory == nil || s.Uptime == nil {
		t.Fatalf("first sample: %+v, %v", s, err)
	}
	if s.Swap != nil || s.Load1 != nil || s.DiskRead != nil || s.NetRx != nil {
		t.Fatal("unsupported values must remain unavailable")
	}
	// A new collector after restart must establish its own CPU baseline.
	restarted, _ := NewCollector().Sample(now)
	if restarted.CPU != nil {
		t.Fatal("restart reused a CPU baseline")
	}
}
