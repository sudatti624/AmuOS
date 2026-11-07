//go:build !linux && !windows

package agent

import (
	"time"

	"github.com/sudatti624/AmuOS/internal/metrics"
)

type unsupportedCollector struct{}

// NewCollector returns a collector that reports ErrUnsupported.
func NewCollector() Collector { return unsupportedCollector{} }

func (unsupportedCollector) HostInfo(time.Time) (metrics.HostInfo, error) {
	return metrics.HostInfo{}, ErrUnsupported
}

func (unsupportedCollector) Sample(time.Time) (metrics.Sample, error) {
	return metrics.Sample{}, ErrUnsupported
}
