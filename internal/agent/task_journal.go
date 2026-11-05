package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const taskJournalRetention = 30 * 24 * time.Hour
const taskJournalSweepInterval = time.Hour
const taskJournalLimit = 10000

type journalSignature struct{ size, modified int64 }

// The high-water mark survives restarts: a clock rollback postpones deletion.
// A corrupt clock record disables deletion, but not reporting or monitoring.
func (a *Agent) journalSweep(now time.Time) (due, allow bool, err error) {
	if !a.nextTaskSweep.IsZero() && now.Before(a.nextTaskSweep) {
		return false, false, nil
	}
	a.nextTaskSweep = now.Add(taskJournalSweepInterval)
	path := a.path("task-journal-clock.json")
	var clock struct {
		LastObserved int64 `json:"last_observed"`
	}
	data, err := readPrivate(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, false, errors.New("task journal clock is unreadable")
	}
	if err == nil && (json.Unmarshal(data, &clock) != nil || clock.LastObserved <= 0) {
		return true, false, errors.New("task journal clock is invalid")
	}
	allow = now.Unix() >= clock.LastObserved
	clock.LastObserved = max(now.Unix(), clock.LastObserved)
	data, err = json.Marshal(clock)
	if err != nil {
		return true, false, err
	}
	if err := writeAtomic(path, data); err != nil {
		return true, false, errors.New("task journal clock could not be saved")
	}
	return true, allow, nil
}

func (a *Agent) scanTaskJournal(ctx context.Context) error {
	directory := a.path("tasks")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return errors.New("task journal directory is unreadable")
	}
	if a.taskAckCache == nil {
		a.taskAckCache = map[string]journalSignature{}
	}
	now := a.clock()
	due, allow, sweepErr := a.journalSweep(now)
	problems := 0
	warn := func(reason string) {
		problems++
		if problems <= 20 {
			slog.Warn("task journal record skipped", "reason", reason)
		}
	}
	if sweepErr != nil {
		warn(sweepErr.Error())
	}
	remaining := len(entries)
	if remaining >= taskJournalLimit {
		slog.Warn("task journal is at capacity before retention check", "records", remaining, "limit", taskJournalLimit)
	}
	// Rotate pending reports so a slow failing report cannot starve all later
	// records across successive bounded task synchronization cycles.
	start := sort.Search(len(entries), func(i int) bool { return entries[i].Name() > a.taskReportCursor })
	entries = append(entries[start:], entries[:start]...)
	seen := map[string]bool{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		path := filepath.Join(directory, name)
		seen[name] = true
		if strings.HasPrefix(name, ".tmp-") {
			if _, err := readPrivate(path); err != nil {
				warn("temporary record unreadable")
				continue
			}
			if err := os.Remove(path); err != nil {
				warn("temporary record removal failed")
				continue
			}
			remaining--
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			warn("unexpected file type")
			continue
		}
		info, err := entry.Info()
		if err != nil {
			warn("record metadata unreadable")
			continue
		}
		if info.Size() > 64*1024 {
			warn("record exceeds size limit")
			continue
		}
		signature := journalSignature{info.Size(), info.ModTime().UnixNano()}
		if previous, ok := a.taskAckCache[name]; ok && previous == signature && !due {
			continue
		}
		delete(a.taskAckCache, name)
		data, err := readPrivate(path)
		if err != nil {
			warn("record unreadable")
			continue
		}
		var local localTask
		if json.Unmarshal(data, &local) != nil {
			warn("invalid JSON")
			continue
		}
		if !safeTaskID(local.Delivery.ID) || name != local.Delivery.ID+".json" || local.Delivery.Operation != "collect" || len(local.Delivery.InputHash) != 64 || local.Delivery.AttemptID == "" || local.TerminalAt < 0 || local.AcknowledgedAt < 0 || (local.Outcome != "" && local.Outcome != "unknown" && local.Outcome != "succeeded" && local.Outcome != "failed") || (local.Acknowledged && local.Outcome == "") {
			warn("invalid record fields")
			continue
		}
		if local.Acknowledged {
			// Legacy records are retained for another full 30 days from the first
			// observation, rather than guessing their acknowledgement time.
			if local.AcknowledgedAt == 0 && due && allow {
				local.AcknowledgedAt = max(now.Unix(), local.TerminalAt, local.Delivery.RunDeadline)
				if err := a.saveTask(local); err != nil {
					warn("legacy timestamp save failed")
					continue
				}
				if info, err := os.Stat(path); err == nil {
					signature = journalSignature{info.Size(), info.ModTime().UnixNano()}
				}
			}
			anchor := max(local.TerminalAt, local.AcknowledgedAt, local.Delivery.RunDeadline)
			if due && allow && local.Outcome != "unknown" && local.ServerState != "unknown" && local.AcknowledgedAt > 0 && anchor < now.Add(-taskJournalRetention).Unix() {
				if err := os.Remove(path); err != nil {
					warn("expired record removal failed")
					continue
				}
				remaining--
				continue
			}
			a.taskAckCache[name] = signature
			continue
		}
		if local.Outcome == "" {
			local.Outcome = "unknown"
			if err := a.saveTask(local); err != nil {
				warn("unknown result save failed")
				continue
			}
		}
		a.taskReportCursor = name
		reportCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = a.reportTask(reportCtx, &local)
		cancel()
		if err != nil {
			var status statusError
			if errors.Is(err, ErrRejected) || (errors.As(err, &status) && status.status == 401) {
				return ErrRejected
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			warn("result report failed")
			continue
		}
	}
	for name := range a.taskAckCache {
		if !seen[name] {
			delete(a.taskAckCache, name)
		}
	}
	if problems > 20 {
		slog.Warn("additional task journal warnings suppressed", "count", problems-20)
	}
	if remaining >= taskJournalLimit {
		slog.Warn("task journal is full; new tasks are disabled", "records", remaining, "limit", taskJournalLimit)
		return errors.New("task journal is full")
	}
	if problems > 0 {
		slog.Warn("task journal has records requiring attention", "records", problems)
	}
	return nil
}
