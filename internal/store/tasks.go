package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

// Task is a persisted request for read-only work on one host.
type Task struct {
	ID         string `json:"id"`
	Operation  string `json:"operation"`
	HostID     string `json:"host_id"`
	State      string `json:"state"`
	Attempt    int    `json:"attempt"`
	Reason     string `json:"reason"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	FinishedAt *int64 `json:"finished_at"`
	// RetryOf names the task this one re-runs; the ID is kept after the
	// original is deleted. RetriedBy names the task that re-ran this one.
	RetryOf   *string `json:"retry_of"`
	RetriedBy *string `json:"retried_by"`
	Replayed  bool    `json:"-"`
}

const (
	taskAcceptWindow = 60 * time.Second
	taskRetention    = 30 * 24 * time.Hour
	// Bounds the queue a single host can accumulate before Agents drain it.
	taskOpenLimit = 10
	taskListLimit = 100
	taskBatch     = 1000
	// A chain of re-runs may hold at most this many retries.
	taskRetryLimit = 3
)

const taskColumns = "id,operation,host_id,state,attempt,reason,created_at,updated_at,finished_at,retry_of,(SELECT r.id FROM tasks r WHERE r.retry_of=tasks.id)"

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var finished sql.NullInt64
	var retryOf, retriedBy sql.NullString
	err := row.Scan(&t.ID, &t.Operation, &t.HostID, &t.State, &t.Attempt, &t.Reason, &t.CreatedAt, &t.UpdatedAt, &finished, &retryOf, &retriedBy)
	if finished.Valid {
		t.FinishedAt = &finished.Int64
	}
	if retryOf.Valid {
		t.RetryOf = &retryOf.String
	}
	if retriedBy.Valid {
		t.RetriedBy = &retriedBy.String
	}
	return t, err
}

// taskInputText is what an idempotency key was first used for. A retry also
// binds the task it re-runs, so one key cannot retry two different tasks.
func taskInputText(operation, hostID, retryOf string) string {
	if retryOf == "" {
		return operation + "\x00" + hostID
	}
	return operation + "\x00" + hostID + "\x00retry\x00" + retryOf
}

// hostState hides hosts outside the caller's read scope as not found ("").
func hostState(tx *sql.Tx, p rbac.Principal, hostID string) (string, error) {
	var state string
	err := tx.QueryRow("SELECT state FROM hosts WHERE id=?", hostID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) || state == "deleted" || !p.Allows(rbac.HostRead, hostID) {
		return "", nil
	}
	return state, err
}

func (s *Store) CreateTask(ctx context.Context, token, csrf, hostID, operation string, now time.Time) (Task, error) {
	if operation != "collect" {
		return Task{}, ErrInput
	}
	return s.createTask(ctx, token, csrf, hostID, operation, "", now)
}

// RetryTask re-runs a finished or unknown task as a new task that refers to
// it. The original keeps its state, so an unknown result is never taken as a
// failure. Each task is re-run at most once and a chain holds at most
// taskRetryLimit retries, so a failing host is not retried without bound.
func (s *Store) RetryTask(ctx context.Context, token, csrf, id string, now time.Time) (Task, error) {
	return s.createTask(ctx, token, csrf, "", "", id, now)
}

func (s *Store) createTask(ctx context.Context, token, csrf, hostID, operation, retryOf string, now time.Time) (Task, error) {
	key := idempotencyKey(ctx)
	if !ValidIdempotencyKey(key) {
		return Task{}, ErrInput
	}
	action := "task.create"
	if retryOf != "" {
		action = "task.retry"
	}
	var task Task
	var rejected error
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, hash, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !security.EqualDigest(csrf, hash) {
			rejected = ErrDenied
			return audit(ctx, tx, now, p.ID, action, "", "", "denied", "invalid_csrf")
		}
		if retryOf != "" {
			original, err := scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM tasks WHERE id=?", retryOf))
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !p.Allows(rbac.HostRead, original.HostID)) {
				rejected = ErrNotFound
				return audit(ctx, tx, now, p.ID, action, "", "", "denied", "not_found")
			}
			if err != nil {
				return err
			}
			hostID, operation = original.HostID, original.Operation
		}
		state, err := hostState(tx, p, hostID)
		if err != nil {
			return err
		}
		if state == "" {
			rejected = ErrNotFound
			return audit(ctx, tx, now, p.ID, action, "", "", "denied", "not_found")
		}
		if !p.Allows(rbac.Collect, hostID) {
			rejected = ErrDenied
			return audit(ctx, tx, now, p.ID, action, hostID, hostID, "denied", "forbidden")
		}
		if state == "revoked" {
			rejected = ErrConflict
			return audit(ctx, tx, now, p.ID, action, hostID, hostID, "denied", "host_revoked")
		}
		var input []byte
		err = tx.QueryRow("SELECT input_hash FROM tasks WHERE requester_id=? AND operation=? AND host_id=? AND key_hash=?", p.ID, operation, hostID, security.Digest(key)).Scan(&input)
		if err == nil {
			// Same requester, operation, host and key: return the original task.
			if !security.EqualDigest(taskInputText(operation, hostID, retryOf), input) {
				rejected = ErrConflict
				return audit(ctx, tx, now, p.ID, action, hostID, hostID, "denied", "idempotency_input_conflict")
			}
			task, err = scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM tasks WHERE requester_id=? AND operation=? AND host_id=? AND key_hash=?", p.ID, operation, hostID, security.Digest(key)))
			if err != nil {
				return err
			}
			task.Replayed = true
			return auditWithDetails(ctx, tx, now, p.ID, action+".replay", task.ID, hostID, "success", "", map[string]any{"replayed": true, "task_id": task.ID})
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if retryOf != "" {
			if reason, err := retryRefusal(tx, retryOf); err != nil {
				return err
			} else if reason != "" {
				rejected = ErrConflict
				return audit(ctx, tx, now, p.ID, action, retryOf, hostID, "denied", reason)
			}
		}
		var open int
		if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE host_id=? AND state IN ('queued','dispatching','running')", hostID).Scan(&open); err != nil {
			return err
		}
		if open >= taskOpenLimit {
			rejected = ErrConflict
			return audit(ctx, tx, now, p.ID, action, hostID, hostID, "denied", "task_limit")
		}
		task = Task{ID: security.Token(), Operation: operation, HostID: hostID, State: "queued", CreatedAt: now.Unix(), UpdatedAt: now.Unix()}
		var reference any
		if retryOf != "" {
			task.RetryOf, reference = &retryOf, retryOf
		}
		if _, err := tx.Exec("INSERT INTO tasks(id,requester_id,operation,host_id,input_hash,key_hash,state,accept_deadline,request_id,created_at,updated_at,retry_of) VALUES(?,?,?,?,?,?,'queued',?,?,?,?,?)",
			task.ID, p.ID, operation, hostID, security.Digest(taskInputText(operation, hostID, retryOf)), security.Digest(key), now.Add(taskAcceptWindow).Unix(), RequestID(ctx), now.Unix(), now.Unix(), reference); err != nil {
			return err
		}
		return auditWithDetails(ctx, tx, now, p.ID, action, task.ID, hostID, "success", "", map[string]any{"before": nil, "after": map[string]any{"operation": operation, "state": "queued", "accept_deadline": now.Add(taskAcceptWindow).Unix(), "retry_of": reference}})
	})
	if err != nil {
		return Task{}, err
	}
	if rejected != nil {
		return Task{}, rejected
	}
	return task, nil
}

// retryRefusal explains why a task cannot be re-run now, or returns "".
func retryRefusal(tx *sql.Tx, id string) (string, error) {
	var state string
	if err := tx.QueryRow("SELECT state FROM tasks WHERE id=?", id).Scan(&state); err != nil {
		return "", err
	}
	if state == "queued" || state == "dispatching" || state == "running" {
		return "task_" + state, nil
	}
	var retried int
	if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE retry_of=?", id).Scan(&retried); err != nil {
		return "", err
	}
	if retried > 0 {
		return "already_retried", nil
	}
	// Count the retries above this task. A deleted original ends the walk,
	// so a chain can grow again only after its old tasks have expired.
	depth := 0
	for current := id; depth < taskRetryLimit; depth++ {
		var parent sql.NullString
		err := tx.QueryRow("SELECT retry_of FROM tasks WHERE id=?", current).Scan(&parent)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !parent.Valid) {
			break
		}
		if err != nil {
			return "", err
		}
		current = parent.String
	}
	if depth >= taskRetryLimit {
		return "retry_limit", nil
	}
	return "", nil
}

// Tasks lists the newest tasks on hosts the caller can read.
func (s *Store) Tasks(ctx context.Context, token string, now time.Time) ([]Task, error) {
	tasks := []Task{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		rows, err := tx.Query("SELECT " + taskColumns + " FROM tasks ORDER BY created_at DESC, id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() && len(tasks) < taskListLimit {
			t, err := scanTask(rows)
			if err != nil {
				return err
			}
			if p.Allows(rbac.HostRead, t.HostID) {
				tasks = append(tasks, t)
			}
		}
		return rows.Err()
	})
	return tasks, err
}

func (s *Store) Task(ctx context.Context, token, id string, now time.Time) (Task, error) {
	var task Task
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		task, err = scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM tasks WHERE id=?", id))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !p.Allows(rbac.HostRead, task.HostID)) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

// changeTask applies one manual transition. Tasks outside the caller's read
// scope are reported as not found; a state that cannot change is a conflict.
func (s *Store) changeTask(ctx context.Context, token, csrf, id, action string, permission rbac.Action, from, to, reason string, details map[string]any, now time.Time) (Task, error) {
	var task Task
	var rejected error
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, hash, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !security.EqualDigest(csrf, hash) {
			rejected = ErrDenied
			return audit(ctx, tx, now, p.ID, action, "", "", "denied", "invalid_csrf")
		}
		task, err = scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM tasks WHERE id=?", id))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !p.Allows(rbac.HostRead, task.HostID)) {
			rejected = ErrNotFound
			return audit(ctx, tx, now, p.ID, action, "", "", "denied", "not_found")
		}
		if err != nil {
			return err
		}
		if !p.Allows(permission, task.HostID) {
			rejected = ErrDenied
			return audit(ctx, tx, now, p.ID, action, task.ID, task.HostID, "denied", "forbidden")
		}
		if task.State != from {
			rejected = ErrConflict
			return audit(ctx, tx, now, p.ID, action, task.ID, task.HostID, "denied", "state_"+task.State)
		}
		var resolver any
		if action == "task.resolve" {
			resolver = p.ID
		}
		if _, err := tx.Exec("UPDATE tasks SET state=?,reason=?,updated_at=?,finished_at=?,resolved_by=coalesce(?,resolved_by) WHERE id=? AND state=?", to, reason, now.Unix(), now.Unix(), resolver, task.ID, from); err != nil {
			return err
		}
		before := task.State
		task.State, task.Reason, task.UpdatedAt = to, reason, now.Unix()
		finished := now.Unix()
		task.FinishedAt = &finished
		details["before"] = map[string]any{"state": before}
		details["after"] = map[string]any{"state": to, "reason": reason}
		return auditWithDetails(ctx, tx, now, p.ID, action, task.ID, task.HostID, "success", "", details)
	})
	if err != nil {
		return Task{}, err
	}
	if rejected != nil {
		return Task{}, rejected
	}
	return task, nil
}

// CancelTask only cancels work that was never handed to an Agent; once
// dispatched, whether it ran cannot be confirmed from here.
func (s *Store) CancelTask(ctx context.Context, token, csrf, id string, now time.Time) (Task, error) {
	return s.changeTask(ctx, token, csrf, id, "task.cancel", rbac.Collect, "queued", "cancelled", "cancelled_by_user", map[string]any{}, now)
}

// ResolveTask closes an unknown task after a person checked the host. The
// evidence is required and kept in the audit log; nothing is re-run.
func (s *Store) ResolveTask(ctx context.Context, token, csrf, id, resolution, evidence string, now time.Time) (Task, error) {
	if (resolution != "succeeded" && resolution != "failed") || len(evidence) < 1 || len(evidence) > 500 {
		return Task{}, ErrInput
	}
	return s.changeTask(ctx, token, csrf, id, "task.resolve", rbac.HostManage, "unknown", resolution, "resolved_manually", map[string]any{"evidence": evidence}, now)
}

// RunTaskMaintenance times out tasks nobody accepted, marks dispatched tasks
// past their deadline as unknown, and removes finished tasks after 30 days.
// Unknown tasks are kept until a person resolves them.
func (s *Store) RunTaskMaintenance(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		steps := []struct {
			action, query string
			args          []any
		}{
			{"task.timeout", "UPDATE tasks SET state='timed_out',reason='not_dispatched',updated_at=?,finished_at=? WHERE id IN (SELECT id FROM tasks WHERE state='queued' AND accept_deadline<=? ORDER BY accept_deadline LIMIT ?)", []any{now.Unix(), now.Unix(), now.Unix(), taskBatch}},
			{"task.unknown", "UPDATE tasks SET state='unknown',reason='deadline_passed',updated_at=? WHERE id IN (SELECT id FROM tasks WHERE state IN ('dispatching','running') AND run_deadline<=? ORDER BY run_deadline LIMIT ?)", []any{now.Unix(), now.Unix(), taskBatch}},
			{"task.expire", "DELETE FROM tasks WHERE id IN (SELECT id FROM tasks WHERE finished_at IS NOT NULL AND finished_at<=? ORDER BY finished_at LIMIT ?)", []any{now.Add(-taskRetention).Unix(), taskBatch}},
		}
		for _, step := range steps {
			result, err := tx.ExecContext(ctx, step.query, step.args...)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count > 0 {
				if err := auditWithDetails(ctx, tx, now, "system", step.action, "tasks", "", "success", "", map[string]any{"count": count}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// holdOpenTasks never guesses the outcome of work in flight. It runs at
// startup (the previous process may have stopped mid-dispatch) and on a
// restored database, where even queued tasks are held for a person to check.
func holdOpenTasks(ctx context.Context, tx *sql.Tx, now time.Time, states, reason string) error {
	result, err := tx.ExecContext(ctx, "UPDATE tasks SET state='unknown',reason=?,updated_at=? WHERE state IN ("+states+")", reason, now.Unix())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	return auditWithDetails(ctx, tx, now, "local", "task.hold", "tasks", "", "unknown", reason, map[string]any{"count": count})
}

func (s *Store) RecoverTasks(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		return holdOpenTasks(ctx, tx, now, "'dispatching','running'", "control_plane_restarted")
	})
}
