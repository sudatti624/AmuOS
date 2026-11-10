package store

import (
	"context"
	"errors"
	"github.com/sudatti624/AmuOS/internal/fileops"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"strings"
	"testing"
)

func TestFileTaskAuthorizationDeliveryReplayAndAudit(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	a := enrollAgent(t, s, owner, host, ca)
	ctx := context.Background()
	if _, err := s.Heartbeat(ctx, a.serial, a.credential.AgentID, "dev", []string{"files_v1"}, testTime); err != nil {
		t.Fatal(err)
	}
	roots := []FileRoot{{Alias: "work", ReadRoles: []rbac.Role{rbac.Admin, rbac.Operator, rbac.Viewer}, WriteRoles: []rbac.Role{rbac.Admin, rbac.Operator}}}
	if err := s.SetFileRoots(ctx, owner.Token, owner.CSRF, host.ID, roots, testTime); err != nil {
		t.Fatal(err)
	}
	_, viewer := inviteUser(t, s, owner, "file-viewer", rbac.Grant{Role: rbac.Viewer, HostID: host.ID})
	_, operator := inviteUser(t, s, owner, "file-operator", rbac.Grant{Role: rbac.Operator, HostID: host.ID})
	q := fileops.Request{Root: "work", Action: "create", Path: "hello", Data: []byte("sensitive-file-body")}
	keyed := WithIdempotencyKey(ctx, "file-request-key-00001")
	if _, err := s.CreateFileTask(keyed, viewer.Token, viewer.CSRF, host.ID, q, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer write %v", err)
	}
	task, err := s.CreateFileTask(keyed, operator.Token, operator.CSRF, host.ID, q, testTime)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateFileTask(keyed, operator.Token, operator.CSRF, host.ID, q, testTime)
	if err != nil || replay.ID != task.ID || !replay.Replayed {
		t.Fatalf("replay %+v %v", replay, err)
	}
	q.Data = []byte("different")
	if _, err := s.CreateFileTask(keyed, operator.Token, operator.CSRF, host.ID, q, testTime); !errors.Is(err, ErrConflict) {
		t.Fatal("input conflict", err)
	}
	d, err := s.PollTasks(ctx, a.serial, a.credential.AgentID, nil, testTime)
	if err != nil || d == nil || d.File == nil || d.Operation != "file" {
		t.Fatalf("delivery %+v %v", d, err)
	}
	receipt := TaskReceipt{ID: d.ID, AttemptID: d.AttemptID, InputHash: d.InputHash}
	if _, err := s.PollTasks(ctx, a.serial, a.credential.AgentID, &receipt, testTime); err != nil {
		t.Fatal(err)
	}
	result := TaskResult{TaskReceipt: receipt, Outcome: "succeeded", File: &fileops.Result{Data: []byte("sensitive-file-body")}}
	if err := s.RecordTaskResult(ctx, a.serial, a.credential.AgentID, result, testTime); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTaskResult(ctx, a.serial, a.credential.AgentID, result, testTime); err != nil {
		t.Fatal("result replay", err)
	}
	if _, err := s.FileTaskResult(ctx, operator.Token, task.ID, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FileTaskResult(ctx, viewer.Token, task.ID, testTime); !errors.Is(err, ErrNotFound) {
		t.Fatal("other user result", err)
	}
	if _, err := s.RetryTask(WithIdempotencyKey(ctx, "file-retry-key-00001"), operator.Token, operator.CSRF, task.ID, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal("file retry", err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM audit_events WHERE details LIKE '%sensitive-file-body%'").Scan(&count); err != nil || count != 0 {
		t.Fatal("content in audit", count, err)
	}
	roots[0].WriteRoles = []rbac.Role{rbac.Admin}
	if err := s.SetFileRoots(ctx, owner.Token, owner.CSRF, host.ID, roots, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FileTaskResult(ctx, operator.Token, task.ID, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked result", err)
	}
}
func TestFileRootScopeAndMutationRollback(t *testing.T) {
	s, owner, host := taskFixture(t)
	ctx := context.Background()
	roots := []FileRoot{{Alias: "work", ReadRoles: []rbac.Role{rbac.Admin}, WriteRoles: []rbac.Role{rbac.Admin}}}
	if err := s.SetFileRoots(ctx, owner.Token, "bad", host.ID, roots, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal("csrf", err)
	}
	if err := s.SetFileRoots(ctx, owner.Token, owner.CSRF, host.ID, roots, testTime); err != nil {
		t.Fatal(err)
	}
	_, viewer := inviteUser(t, s, owner, "file-scoped", rbac.Grant{Role: rbac.Viewer, HostID: host.ID})
	if err := s.SetFileRoots(ctx, viewer.Token, viewer.CSRF, host.ID, roots, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal("viewer config", err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER file_audit_failure BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFileRoots(ctx, owner.Token, owner.CSRF, host.ID, nil, testTime); err == nil {
		t.Fatal("audit failure committed")
	}
	got, err := s.FileRoots(ctx, owner.Token, host.ID, testTime)
	if err != nil || len(got) != 1 {
		t.Fatal("policy rollback", got, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER file_audit_failure"); err != nil {
		t.Fatal(err)
	}
	q := fileops.Request{Root: "work", Action: "list", Path: "."}
	task, err := s.CreateFileTask(WithIdempotencyKey(ctx, strings.Repeat("k", 20)), owner.Token, owner.CSRF, host.ID, q, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverTasks(ctx, testTime); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Task(ctx, owner.Token, task.ID, testTime); err != nil || got.State != "queued" {
		t.Fatal(got, err)
	}
}

func TestFileDirectoryScopesAndReceiptRevocation(t *testing.T) {
	s, owner, a, ca := agentFixture(t)
	ctx := context.Background()
	b, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "file-other", testTime)
	if err != nil {
		t.Fatal(err)
	}
	_, user := inviteUser(t, s, owner, "file-mixed", rbac.Grant{Role: rbac.Admin, HostID: a.ID}, rbac.Grant{Role: rbac.Viewer, HostID: b.ID})
	roots := []FileRoot{{Alias: "work", ReadRoles: []rbac.Role{rbac.Admin, rbac.Viewer}, WriteRoles: []rbac.Role{rbac.Admin}}}
	for _, host := range []Host{a, b} {
		if err := s.SetFileRoots(ctx, owner.Token, owner.CSRF, host.ID, roots, testTime); err != nil {
			t.Fatal(err)
		}
	}
	q := fileops.Request{Root: "work", Action: "create", Path: "denied"}
	if _, err := s.CreateFileTask(WithIdempotencyKey(ctx, "file-scope-denied-0001"), user.Token, user.CSRF, b.ID, q, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal("combined role and host scopes", err)
	}
	agent := enrollAgent(t, s, owner, a, ca)
	if _, err := s.Heartbeat(ctx, agent.serial, agent.credential.AgentID, "dev", []string{"files_v1"}, testTime); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateFileTask(WithIdempotencyKey(ctx, "file-receipt-revoke-01"), user.Token, user.CSRF, a.ID, q, testTime)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.PollTasks(ctx, agent.serial, agent.credential.AgentID, nil, testTime)
	if err != nil || d == nil {
		t.Fatal(d, err)
	}
	if err := s.SetUserGrants(ctx, owner.Token, owner.CSRF, user.UserID, []rbac.Grant{{Role: rbac.Viewer, HostID: a.ID}}, testTime); err != nil {
		t.Fatal(err)
	}
	receipt := TaskReceipt{ID: d.ID, AttemptID: d.AttemptID, InputHash: d.InputHash}
	if _, err := s.PollTasks(ctx, agent.serial, agent.credential.AgentID, &receipt, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked receipt accepted", err)
	}
	if got, err := s.Task(ctx, owner.Token, task.ID, testTime); err != nil || got.State != "dispatching" {
		t.Fatal(got, err)
	}
}
