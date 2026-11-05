package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
	"time"
)

type TaskDelivery struct {
	ID             string `json:"id"`
	AttemptID      string `json:"attempt_id"`
	InputHash      string `json:"input_hash"`
	Operation      string `json:"operation"`
	AcceptDeadline int64  `json:"accept_deadline"`
	RunDeadline    int64  `json:"run_deadline"`
}
type TaskReceipt struct {
	ID        string `json:"id"`
	AttemptID string `json:"attempt_id"`
	InputHash string `json:"input_hash"`
}
type TaskResult struct {
	TaskReceipt
	Outcome string `json:"outcome"`
}

// The receipt exposes the confirmed server state so a late result retained as
// unknown cannot be mistaken for a terminal journal entry by the Agent.
type TaskAcknowledgement struct {
	Acknowledged bool   `json:"acknowledged"`
	State        string `json:"state"`
	FinishedAt   *int64 `json:"finished_at"`
}

func (s *Store) TaskResultAcknowledgement(ctx context.Context, serial, agentID string, result TaskResult, now time.Time) (TaskAcknowledgement, error) {
	var receipt TaskAcknowledgement
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, err := agentAuthenticate(ctx, tx, serial, agentID, now)
		if err != nil {
			return err
		}
		err = tx.QueryRow(`SELECT t.state,t.finished_at FROM tasks t JOIN task_attempts a ON a.task_id=t.id WHERE t.id=? AND t.host_id=? AND a.agent_id=? AND a.attempt_id=? AND a.input_hash=? AND a.outcome=?`, result.ID, p.HostID, p.AgentID, result.AttemptID, result.InputHash, result.Outcome).Scan(&receipt.State, &receipt.FinishedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		receipt.Acknowledged = true
		return nil
	})
	return receipt, err
}

func (s *Store) PollTasks(ctx context.Context, serial, agentID string, receipt *TaskReceipt, now time.Time) (*TaskDelivery, error) {
	var delivery *TaskDelivery
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, err := agentAuthenticate(ctx, tx, serial, agentID, now)
		if err != nil {
			return err
		}
		if receipt != nil {
			var state string
			err := tx.QueryRow(`SELECT t.state FROM task_attempts a JOIN tasks t ON t.id=a.task_id WHERE a.task_id=? AND a.attempt_id=? AND a.agent_id=? AND a.input_hash=? AND t.host_id=?`, receipt.ID, receipt.AttemptID, p.AgentID, receipt.InputHash, p.HostID).Scan(&state)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			if state == "dispatching" {
				var deadline int64
				if err := tx.QueryRow("SELECT accept_deadline FROM tasks WHERE id=?", receipt.ID).Scan(&deadline); err != nil {
					return err
				}
				next := "running"
				if now.Unix() >= deadline {
					next = "unknown"
				}
				if _, err := tx.Exec("UPDATE tasks SET state=?,updated_at=? WHERE id=?", next, now.Unix(), receipt.ID); err != nil {
					return err
				}
				return audit(ctx, tx, now, p.AgentID, "task.accept", receipt.ID, p.HostID, next, "")
			}
			return nil
		}
		var d TaskDelivery
		var requester, state string
		var input []byte
		err = tx.QueryRow(`SELECT id,operation,accept_deadline,coalesce(run_deadline,0),requester_id,state,input_hash FROM tasks WHERE host_id=? AND state IN ('queued','dispatching') AND accept_deadline>? ORDER BY created_at,id LIMIT 1`, p.HostID, now.Unix()).Scan(&d.ID, &d.Operation, &d.AcceptDeadline, &d.RunDeadline, &requester, &state, &input)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		owner, err := principal(tx, requester)
		if err != nil {
			return err
		}
		if !owner.Allows(rbac.Collect, p.HostID) {
			next := "failed"
			var finished any = now.Unix()
			if state == "dispatching" {
				next = "unknown"
				finished = nil
			}
			if _, err := tx.Exec("UPDATE tasks SET state=?,reason='permission_revoked',finished_at=?,updated_at=? WHERE id=?", next, finished, now.Unix(), d.ID); err != nil {
				return err
			}
			return audit(ctx, tx, now, requester, "task.dispatch", d.ID, p.HostID, next, "permission_revoked")
		}
		if state == "dispatching" {
			if err := tx.QueryRow("SELECT attempt_id,input_hash FROM task_attempts WHERE task_id=? AND agent_id=?", d.ID, p.AgentID).Scan(&d.AttemptID, &d.InputHash); err != nil {
				return err
			}
		} else {
			d.AttemptID = security.Token()
			d.InputHash = hex.EncodeToString(input)
			d.RunDeadline = now.Add(30 * time.Second).Unix()
			if _, err := tx.Exec("INSERT INTO task_attempts(task_id,attempt_id,agent_id,input_hash) VALUES(?,?,?,?)", d.ID, d.AttemptID, p.AgentID, d.InputHash); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE tasks SET state='dispatching',attempt=attempt+1,run_deadline=?,updated_at=? WHERE id=?", d.RunDeadline, now.Unix(), d.ID); err != nil {
				return err
			}
			if err := audit(ctx, tx, now, requester, "task.dispatch", d.ID, p.HostID, "dispatching", ""); err != nil {
				return err
			}
		}
		delivery = &d
		return nil
	})
	return delivery, err
}

func (s *Store) RecordTaskResult(ctx context.Context, serial, agentID string, result TaskResult, now time.Time) error {
	if result.ID == "" || result.AttemptID == "" || len(result.InputHash) != 64 || (result.Outcome != "succeeded" && result.Outcome != "failed" && result.Outcome != "unknown") {
		return ErrInput
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		p, err := agentAuthenticate(ctx, tx, serial, agentID, now)
		if err != nil {
			return err
		}
		var previous sql.NullString
		var state string
		var deadline int64
		err = tx.QueryRow(`SELECT a.outcome,t.state,t.run_deadline FROM task_attempts a JOIN tasks t ON t.id=a.task_id WHERE a.task_id=? AND a.agent_id=? AND a.attempt_id=? AND a.input_hash=? AND t.host_id=?`, result.ID, p.AgentID, result.AttemptID, result.InputHash, p.HostID).Scan(&previous, &state, &deadline)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if previous.Valid {
			if previous.String != result.Outcome {
				return ErrConflict
			}
			return nil
		}
		if state != "running" && state != "unknown" && state != "dispatching" {
			return ErrConflict
		}
		if state == "dispatching" && result.Outcome != "unknown" {
			return ErrConflict
		}
		next := result.Outcome
		if now.Unix() >= deadline {
			next = "unknown"
		}
		var finished any
		if next != "unknown" {
			finished = now.Unix()
		}
		if _, err := tx.Exec("UPDATE task_attempts SET outcome=? WHERE task_id=?", result.Outcome, result.ID); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE tasks SET state=?,updated_at=?,finished_at=? WHERE id=?", next, now.Unix(), finished, result.ID); err != nil {
			return err
		}
		return audit(ctx, tx, now, p.AgentID, "task.result", result.ID, p.HostID, next, "")
	})
}
