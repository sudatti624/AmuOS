package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/sudatti624/AmuOS/internal/metrics"
	"os/exec"
	"runtime"
	"time"
)

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 256*1024 {
		return 0, metrics.ErrInvalid
	}
	return b.Buffer.Write(p)
}

func collectSystemd(ctx context.Context, now time.Time) metrics.SystemdSnapshot {
	result := metrics.SystemdSnapshot{CollectedAt: now.Unix(), Units: []metrics.ServiceState{}}
	if runtime.GOOS != "linux" {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/systemctl", "list-units", "--type=service", "--all", "--no-pager", "--output=json")
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "SYSTEMD_PAGER=cat"}
	output := &limitedOutput{}
	command.Stdout = output
	if command.Run() != nil {
		return result
	}
	// Descriptions and unit properties are intentionally never forwarded.
	var units []struct {
		Unit   string `json:"unit"`
		Load   string `json:"load"`
		Active string `json:"active"`
		Sub    string `json:"sub"`
	}
	if json.Unmarshal(output.Bytes(), &units) != nil {
		return result
	}
	result.Available = true
	for i, u := range units {
		if i >= 256 {
			result.Truncated = true
			break
		}
		result.Units = append(result.Units, metrics.ServiceState{Unit: u.Unit, Load: u.Load, Active: u.Active, Sub: u.Sub})
	}
	if result.Validate() != nil {
		return metrics.SystemdSnapshot{CollectedAt: now.Unix(), Units: []metrics.ServiceState{}}
	}
	return result
}
