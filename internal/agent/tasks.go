package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/sudatti624/AmuOS/internal/store"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type localTask struct {
	Delivery       store.TaskDelivery `json:"delivery"`
	Outcome        string             `json:"outcome"`
	Acknowledged   bool               `json:"acknowledged"`
	TerminalAt     int64              `json:"terminal_at,omitempty"`
	AcknowledgedAt int64              `json:"acknowledged_at,omitempty"`
	ServerState    string             `json:"server_state,omitempty"`
}

func (a *Agent) syncTasks(ctx context.Context) error {
	cert, _, _, err := a.loadCredential()
	if err != nil {
		return err
	}
	client := a.client(&cert)
	directory := a.path("tasks")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := a.scanTaskJournal(ctx); err != nil {
		a.tasksBlocked = true
		return err
	}
	a.tasksBlocked = false
	var reply struct {
		Task *store.TaskDelivery `json:"task"`
	}
	if err := post(ctx, client, a.cfg.AgentServer+"/agent/v1/tasks/poll", map[string]any{}, &reply); err != nil {
		return err
	}
	if reply.Task != nil {
		d := *reply.Task
		if !safeTaskID(d.ID) || d.Operation != "collect" || len(d.InputHash) != 64 || d.AttemptID == "" {
			return errors.New("invalid task delivery")
		}
		path := filepath.Join(directory, d.ID+".json")
		if data, err := readPrivate(path); err == nil {
			var old localTask
			if err := json.Unmarshal(data, &old); err != nil {
				return err
			}
			if old.Delivery.AttemptID != d.AttemptID || old.Delivery.InputHash != d.InputHash {
				return errors.New("task delivery conflicts with journal")
			}
			return a.reportTask(ctx, &old)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		local := localTask{Delivery: d}
		if a.clock().Unix() >= d.AcceptDeadline || a.clock().Unix() >= d.RunDeadline {
			local.Outcome = "unknown"
		} else {
			if err := a.saveTask(local); err != nil {
				return err
			}
			receipt := store.TaskReceipt{ID: d.ID, AttemptID: d.AttemptID, InputHash: d.InputHash}
			if err := post(ctx, client, a.cfg.AgentServer+"/agent/v1/tasks/poll", map[string]any{"receipt": receipt}, nil); err != nil {
				return err
			}
			local.Outcome = "failed"
			taskCtx, cancel := context.WithDeadline(ctx, time.Unix(d.RunDeadline, 0))
			if a.supported && a.clock().Unix() < d.AcceptDeadline && a.clock().Unix() < d.RunDeadline {
				info, infoErr := a.cfg.Collector.HostInfo(a.clock())
				sample, sampleErr := a.cfg.Collector.Sample(a.clock())
				a.seq++
				sample.Seq = a.seq
				state := collectSystemd(taskCtx, a.clock())
				if infoErr == nil && sampleErr == nil {
					local.Outcome = "unknown"
				}
				if infoErr == nil && sampleErr == nil && post(taskCtx, client, a.cfg.AgentServer+"/agent/v1/host-info", info, nil) == nil && post(taskCtx, client, a.cfg.AgentServer+"/agent/v1/samples", map[string]any{"run_id": a.runID, "samples": []any{sample}}, nil) == nil && post(taskCtx, client, a.cfg.AgentServer+"/agent/v1/systemd", state, nil) == nil {
					local.Outcome = "succeeded"
				}
			}
			cancel()
		}
		if err := a.saveTask(local); err != nil {
			return err
		}
		if err := a.reportTask(ctx, &local); err != nil {
			return err
		}
	}
	if a.supported {
		return post(ctx, client, a.cfg.AgentServer+"/agent/v1/systemd", collectSystemd(ctx, a.clock()), nil)
	}
	return nil
}

func safeTaskID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func (a *Agent) saveTask(local localTask) error {
	if !safeTaskID(local.Delivery.ID) {
		return errors.New("invalid task journal ID")
	}
	if !local.Acknowledged && local.TerminalAt == 0 && (local.Outcome == "succeeded" || local.Outcome == "failed") {
		local.TerminalAt = max(a.clock().Unix(), local.Delivery.RunDeadline)
	}
	data, err := json.Marshal(local)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(a.path("tasks"), local.Delivery.ID+".json"), data); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		for _, path := range []string{a.path("tasks"), a.cfg.StateDir} {
			dir, err := os.Open(path)
			if err != nil {
				return err
			}
			err = errors.Join(dir.Sync(), dir.Close())
			if err != nil {
				return err
			}
		}
	}
	return nil
}
func (a *Agent) reportTask(ctx context.Context, local *localTask) error {
	cert, _, _, err := a.loadCredential()
	if err != nil {
		return err
	}
	result := store.TaskResult{TaskReceipt: store.TaskReceipt{ID: local.Delivery.ID, AttemptID: local.Delivery.AttemptID, InputHash: local.Delivery.InputHash}, Outcome: local.Outcome}
	var ack struct {
		Acknowledged bool   `json:"acknowledged"`
		State        string `json:"state"`
		FinishedAt   *int64 `json:"finished_at"`
	}
	if err := post(ctx, a.client(&cert), a.cfg.AgentServer+"/agent/v1/tasks/"+local.Delivery.ID+"/result", result, &ack); err != nil {
		return err
	}
	if !ack.Acknowledged {
		return errors.New("task result not acknowledged")
	}
	local.Acknowledged = true
	local.ServerState = ack.State
	if ack.FinishedAt != nil {
		local.TerminalAt = max(local.TerminalAt, *ack.FinishedAt)
	}
	if local.TerminalAt == 0 && (local.Outcome == "succeeded" || local.Outcome == "failed") {
		local.TerminalAt = max(a.clock().Unix(), local.Delivery.RunDeadline)
	}
	if local.AcknowledgedAt == 0 {
		local.AcknowledgedAt = max(a.clock().Unix(), local.TerminalAt, local.Delivery.RunDeadline)
	}
	return a.saveTask(*local)
}
