package store

import (
	"context"
	"errors"
	"github.com/sudatti624/AmuOS/internal/metrics"
	"testing"
	"time"
)

func TestTaskDeliveryCredentialDeadlineReplayAndRestart(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	agent := enrollAgent(t, s, owner, host, ca)
	ctx := context.Background()
	now := testTime
	task, err := newTask(t, s, owner, host.ID, "task-delivery-key-0001", now)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := s.PollTasks(ctx, agent.serial, agent.credential.AgentID, nil, now)
	if err != nil || delivery == nil || delivery.ID != task.ID {
		t.Fatalf("delivery %v %v", delivery, err)
	}
	again, err := s.PollTasks(ctx, agent.serial, agent.credential.AgentID, nil, now)
	if err != nil || *again != *delivery {
		t.Fatalf("replay %v %v", again, err)
	}
	receipt := TaskReceipt{ID: delivery.ID, AttemptID: delivery.AttemptID, InputHash: delivery.InputHash}
	if _, err := s.PollTasks(ctx, agent.serial, agent.credential.AgentID, &receipt, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverTasks(ctx, now); err != nil {
		t.Fatal(err)
	}
	var database string
	if err := s.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&database); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result := TaskResult{TaskReceipt: receipt, Outcome: "succeeded"}
	if err := s.RecordTaskResult(ctx, agent.serial, agent.credential.AgentID, result, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTaskResult(ctx, agent.serial, agent.credential.AgentID, result, now); err != nil {
		t.Fatal(err)
	}
	result.Outcome = "failed"
	if err := s.RecordTaskResult(ctx, agent.serial, agent.credential.AgentID, result, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict %v", err)
	}
	otherHost, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "other", now)
	if err != nil {
		t.Fatal(err)
	}
	other := enrollAgent(t, s, owner, otherHost, ca)
	if delivery, err := s.PollTasks(ctx, other.serial, other.credential.AgentID, nil, now); err != nil || delivery != nil {
		t.Fatalf("other host poll %+v %v", delivery, err)
	}
	if err := s.RecordTaskResult(ctx, other.serial, other.credential.AgentID, result, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other host %v", err)
	}
	if _, err := newTask(t, s, owner, host.ID, "task-delivery-key-0002", now); err != nil {
		t.Fatal(err)
	}
	if d, err := s.PollTasks(ctx, agent.serial, agent.credential.AgentID, nil, now.Add(time.Minute)); err != nil || d != nil {
		t.Fatalf("expired %v %v", d, err)
	}
	if err := s.RevokeHost(ctx, owner.Token, owner.CSRF, host.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollTasks(ctx, agent.serial, agent.credential.AgentID, nil, now); !errors.Is(err, ErrCredentials) {
		t.Fatalf("revoked poll %v", err)
	}
	if err := s.RecordTaskResult(ctx, agent.serial, agent.credential.AgentID, result, now); !errors.Is(err, ErrCredentials) {
		t.Fatalf("revoked result %v", err)
	}
}

func TestSystemdSnapshotAndDeliveryAuditFailure(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	agent := enrollAgent(t, s, owner, host, ca)
	ctx := context.Background()
	snapshot := metrics.SystemdSnapshot{CollectedAt: testTime.Unix(), Available: true, Units: []metrics.ServiceState{{Unit: "example.service", Load: "loaded", Active: "active", Sub: "running"}}}
	if err := s.RecordSystemd(ctx, agent.serial, agent.credential.AgentID, snapshot, testTime); err != nil {
		t.Fatal(err)
	}
	record, err := s.Systemd(ctx, owner.Token, host.ID, testTime)
	if err != nil || record.ReceivedAt != testTime.Unix() || len(record.Units) != 1 {
		t.Fatalf("systemd %+v %v", record, err)
	}
	snapshot.Units[0].Unit = "invalid"
	if err := s.RecordSystemd(ctx, agent.serial, agent.credential.AgentID, snapshot, testTime); !errors.Is(err, ErrInput) {
		t.Fatalf("invalid %v", err)
	}
	task, err := newTask(t, s, owner, host.ID, "task-delivery-key-0003", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER reject_delivery_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'blocked'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollTasks(ctx, agent.serial, agent.credential.AgentID, nil, testTime); err == nil {
		t.Fatal("dispatched without audit")
	}
	var state string
	if err := s.db.QueryRow("SELECT state FROM tasks WHERE id=?", task.ID).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("rollback %s %v", state, err)
	}
}
