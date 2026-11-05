// Package metrics defines the host information and samples an Agent reports,
// and the limits the Control Plane enforces on them. A missing value is nil,
// never zero, so the UI can show it as unavailable.
package metrics

import (
	"errors"
	"math"
	"unicode/utf8"
)

const (
	// MaxBatch bounds one upload; ten minutes at the shortest interval fits.
	MaxBatch       = 120
	maxText        = 256
	maxFilesystems = 64
	maxInterfaces  = 64
	maxAddresses   = 16
	maxRate        = 1e13 // bytes per second
	maxLoad        = 1e6
)

var ErrInvalid = errors.New("invalid metrics")

// Sample is one collection. CPU, Memory, Swap and Disk are percentages used;
// the I/O fields are bytes per second since the previous sample of the same
// run, nil when there is no previous sample or a counter went backwards.
type Sample struct {
	Seq         int64    `json:"seq"`
	CollectedAt int64    `json:"collected_at"`
	CPU         *float64 `json:"cpu"`
	Load1       *float64 `json:"load1"`
	Load5       *float64 `json:"load5"`
	Load15      *float64 `json:"load15"`
	Memory      *float64 `json:"memory"`
	Swap        *float64 `json:"swap"`
	Disk        *float64 `json:"disk"`
	DiskRead    *float64 `json:"disk_read"`
	DiskWrite   *float64 `json:"disk_write"`
	NetRx       *float64 `json:"net_rx"`
	NetTx       *float64 `json:"net_tx"`
	Uptime      *int64   `json:"uptime"`
}

// Names lists the aggregated series in a fixed order.
var Names = []string{"cpu", "load1", "load5", "load15", "memory", "swap", "disk", "disk_read", "disk_write", "net_rx", "net_tx"}

// Values returns the aggregated series in the order of Names.
func (s Sample) Values() []*float64 {
	return []*float64{s.CPU, s.Load1, s.Load5, s.Load15, s.Memory, s.Swap, s.Disk, s.DiskRead, s.DiskWrite, s.NetRx, s.NetTx}
}

func (s Sample) Validate() error {
	if s.Seq < 1 || s.Seq > 1<<53 || s.CollectedAt <= 0 {
		return ErrInvalid
	}
	for i, v := range s.Values() {
		if v == nil {
			continue
		}
		limit := maxRate
		switch Names[i] {
		case "cpu", "memory", "swap", "disk":
			limit = 100
		case "load1", "load5", "load15":
			limit = maxLoad
		}
		if math.IsNaN(*v) || *v < 0 || *v > limit {
			return ErrInvalid
		}
	}
	if s.Uptime != nil && (*s.Uptime < 0 || *s.Uptime > 1<<40) {
		return ErrInvalid
	}
	return nil
}

type Filesystem struct {
	Mount      string  `json:"mount"`
	Type       string  `json:"type"`
	TotalBytes *uint64 `json:"total_bytes"`
	UsedBytes  *uint64 `json:"used_bytes"`
}

type Interface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

// HostInfo changes rarely and is sent at start and then periodically.
// Empty text and nil numbers mean the Agent could not read the value.
type HostInfo struct {
	CollectedAt int64        `json:"collected_at"`
	Hostname    string       `json:"hostname"`
	OS          string       `json:"os"`
	Kernel      string       `json:"kernel"`
	Arch        string       `json:"arch"`
	CPUModel    string       `json:"cpu_model"`
	CPUCount    *int         `json:"cpu_count"`
	MemoryBytes *uint64      `json:"memory_bytes"`
	SwapBytes   *uint64      `json:"swap_bytes"`
	BootTime    *int64       `json:"boot_time"`
	Filesystems []Filesystem `json:"filesystems"`
	Interfaces  []Interface  `json:"interfaces"`
}

func text(values ...string) bool {
	for _, v := range values {
		if len(v) > maxText || !utf8.ValidString(v) {
			return false
		}
		for _, r := range v {
			if r < 0x20 || r == 0x7f {
				return false
			}
		}
	}
	return true
}

func (h HostInfo) Validate() error {
	if h.CollectedAt <= 0 || !text(h.Hostname, h.OS, h.Kernel, h.Arch, h.CPUModel) ||
		len(h.Filesystems) > maxFilesystems || len(h.Interfaces) > maxInterfaces {
		return ErrInvalid
	}
	if h.CPUCount != nil && (*h.CPUCount < 1 || *h.CPUCount > 1<<16) {
		return ErrInvalid
	}
	if h.BootTime != nil && *h.BootTime <= 0 {
		return ErrInvalid
	}
	for _, f := range h.Filesystems {
		if f.Mount == "" || !text(f.Mount, f.Type) {
			return ErrInvalid
		}
		if f.TotalBytes != nil && f.UsedBytes != nil && *f.UsedBytes > *f.TotalBytes {
			return ErrInvalid
		}
	}
	for _, i := range h.Interfaces {
		if i.Name == "" || len(i.Addresses) > maxAddresses || !text(i.Name) || !text(i.Addresses...) {
			return ErrInvalid
		}
	}
	return nil
}
