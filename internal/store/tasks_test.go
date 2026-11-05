package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
)

func taskFixture(t *testing.T) (*Store, Session, Host) {
	t.Helper()
	s, owner, _ := prepared(t)
	host, err := s.CreateHost(context.Background(), owner.Token, owner.CSRF, "task-host", testTime)
	if err != nil {
		t.Fatal(err)
	}
	return s, owner, host
}

func newTask(t *testing.T, s *Store, session Session, hostID, key string, now time.Time) (Task, error) {
	t.Helper()
	return s.CreateTask(WithIdempotencyKey(context.Background(), key), session.Token, session.CSRF, hostID, "collect", now)
}

func taskState(t *testing.T, s *Store, id string) (state, reason string) {
	t.Helper()
	if err := s.db.QueryRow("SELECT state,reason FROM tasks WHERE id=?", id).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	return state, reason
}

func TestTaskAuthorizationFollowsHostScope(t *testing.T) {
	s, owner, host := taskFixture(t)
	ctx := context.Background()
	other, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "other-host", testTime)
	if err != nil {
		t.Fatal(err)
	}
	_, viewer := inviteUser(t, s, owner, "viewer", rbac.Grant{Role: rbac.Viewer, HostID: host.ID})
	operatorUser, operator := inviteUser(t, s, owner, "operator", rbac.Grant{Role: rbac.Operator, HostID: host.ID})

	if _, err := newTask(t, s, viewer, host.ID, "viewer-key-0000001", testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer created a task: %v", err)
	}
	// Hosts outside the caller's scope look the same as missing hosts.
	for _, id := range []string{other.ID, "missing-host"} {
		if _, err := newTask(t, s, operator, id, "operator-key-00001", testTime); !errors.Is(err, ErrNotFound) {
			t.Fatalf("task outside scope: %v", err)
		}
	}
	bad := operator
	bad.CSRF = "wrong"
	if _, err := newTask(t, s, bad, host.ID, "operator-key-00002", testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("missing CSRF: %v", err)
	}
	if _, err := s.CreateTask(ctx, operator.Token, operator.CSRF, host.ID, "collect", testTime); !errors.Is(err, ErrInput) {
		t.Fatalf("task without key: %v", err)
	}
	if _, err := s.CreateTask(WithIdempotencyKey(ctx, "operator-key-00003"), operator.Token, operator.CSRF, host.ID, "exec", testTime); !errors.Is(err, ErrInput) {
		t.Fatalf("unsupported operation: %v", err)
	}

	task, err := newTask(t, s, operator, host.ID, "operator-key-00004", testTime)
	if err != nil {
		t.Fatal(err)
	}
	ownTask, err := newTask(t, s, owner, other.ID, "owner-key-0000001", testTime)
	if err != nil {
		t.Fatal(err)
	}
	// Viewers can read tasks on their hosts, never on others.
	listed, err := s.Tasks(ctx, viewer.Token, testTime)
	if err != nil || len(listed) != 1 || listed[0].ID != task.ID {
		t.Fatalf("viewer list: %v %v", listed, err)
	}
	if _, err := s.Task(ctx, viewer.Token, ownTask.ID, testTime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("viewer read a task outside scope: %v", err)
	}
	if _, err := s.CancelTask(ctx, viewer.Token, viewer.CSRF, task.ID, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer cancelled: %v", err)
	}
	if _, err := s.CancelTask(ctx, operator.Token, operator.CSRF, ownTask.ID, testTime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("operator cancelled outside scope: %v", err)
	}
	cancelled, err := s.CancelTask(ctx, operator.Token, operator.CSRF, task.ID, testTime)
	if err != nil || cancelled.State != "cancelled" || cancelled.FinishedAt == nil {
		t.Fatalf("cancel: %+v %v", cancelled, err)
	}
	if _, err := s.CancelTask(ctx, operator.Token, operator.CSRF, task.ID, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancelled twice: %v", err)
	}
	// A revoked grant takes effect on the next request.
	if err := s.SetUserGrants(ctx, owner.Token, owner.CSRF, operatorUser.ID, []rbac.Grant{{Role: rbac.Viewer, HostID: host.ID}}, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := newTask(t, s, operator, host.ID, "operator-key-00005", testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("task after the grant was reduced: %v", err)
	}
}

func TestTaskIdempotencyAndQueueLimit(t *testing.T) {
	s, owner, host := taskFixture(t)
	first, err := newTask(t, s, owner, host.ID, "replay-key-0000001", testTime)
	if err != nil || first.State != "queued" || first.Replayed {
		t.Fatalf("create: %+v %v", first, err)
	}
	again, err := newTask(t, s, owner, host.ID, "replay-key-0000001", testTime)
	if err != nil || again.ID != first.ID || !again.Replayed {
		t.Fatalf("replay: %+v %v", again, err)
	}
	for i := 1; i < taskOpenLimit; i++ {
		if _, err := newTask(t, s, owner, host.ID, fmt.Sprintf("limit-key-%08d", i), testTime); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := newTask(t, s, owner, host.ID, "limit-key-overflow", testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("queue limit: %v", err)
	}
	// Replaying an existing task is still allowed at the limit.
	if again, err := newTask(t, s, owner, host.ID, "replay-key-0000001", testTime); err != nil || again.ID != first.ID {
		t.Fatalf("replay at limit: %v", err)
	}
}

func TestTaskDeadlinesAndRetention(t *testing.T) {
	s, owner, host := taskFixture(t)
	ctx := context.Background()
	queued, err := newTask(t, s, owner, host.ID, "deadline-key-00001", testTime)
	if err != nil {
		t.Fatal(err)
	}
	dispatched, err := newTask(t, s, owner, host.ID, "deadline-key-00002", testTime)
	if err != nil {
		t.Fatal(err)
	}
	// Agents arrive in plan item 5; simulate a dispatched attempt directly.
	if _, err := s.db.Exec("UPDATE tasks SET state='running',attempt=1,run_deadline=? WHERE id=?", testTime.Add(30*time.Second).Unix(), dispatched.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RunTaskMaintenance(ctx, testTime.Add(59*time.Second)); err != nil {
		t.Fatal(err)
	}
	if state, _ := taskState(t, s, queued.ID); state != "queued" {
		t.Fatalf("timed out before the deadline: %s", state)
	}
	if state, reason := taskState(t, s, dispatched.ID); state != "unknown" || reason != "deadline_passed" {
		t.Fatalf("dispatched task past deadline: %s %s", state, reason)
	}
	if err := s.RunTaskMaintenance(ctx, testTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if state, reason := taskState(t, s, queued.ID); state != "timed_out" || reason != "not_dispatched" {
		t.Fatalf("queued task past deadline: %s %s", state, reason)
	}
	// Finished tasks are removed after 30 days; unknown tasks wait for a person.
	later := testTime.Add(time.Minute + taskRetention)
	if err := s.RunTaskMaintenance(ctx, later); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM tasks WHERE id=?", queued.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("finished task kept: %d %v", count, err)
	}
	if state, _ := taskState(t, s, dispatched.ID); state != "unknown" {
		t.Fatalf("unknown task removed or changed: %s", state)
	}
	var audits int
	if err := s.db.QueryRow("SELECT count(*) FROM audit_events WHERE actor_id='system' AND action IN ('task.timeout','task.unknown','task.expire')").Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("maintenance audit: %d %v", audits, err)
	}
}

func TestUnknownTasksNeedManualResolution(t *testing.T) {
	s, owner, host := taskFixture(t)
	ctx := context.Background()
	_, operator := inviteUser(t, s, owner, "operator", rbac.Grant{Role: rbac.Operator, HostID: host.ID})
	task, err := newTask(t, s, owner, host.ID, "resolve-key-000001", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveTask(ctx, owner.Token, owner.CSRF, task.ID, "succeeded", "checked", testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("resolved a queued task: %v", err)
	}
	if _, err := s.db.Exec("UPDATE tasks SET state='running',attempt=1,run_deadline=? WHERE id=?", testTime.Add(time.Hour).Unix(), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelTask(ctx, owner.Token, owner.CSRF, task.ID, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancelled a dispatched task: %v", err)
	}
	if err := s.RecoverTasks(ctx, testTime); err != nil {
		t.Fatal(err)
	}
	if state, reason := taskState(t, s, task.ID); state != "unknown" || reason != "control_plane_restarted" {
		t.Fatalf("restart recovery: %s %s", state, reason)
	}
	for _, evidence := range []string{"", string(make([]byte, 501))} {
		if _, err := s.ResolveTask(ctx, owner.Token, owner.CSRF, task.ID, "succeeded", evidence, testTime); !errors.Is(err, ErrInput) {
			t.Fatalf("evidence %d bytes: %v", len(evidence), err)
		}
	}
	if _, err := s.ResolveTask(ctx, owner.Token, owner.CSRF, task.ID, "queued", "checked", testTime); !errors.Is(err, ErrInput) {
		t.Fatalf("invalid resolution: %v", err)
	}
	if _, err := s.ResolveTask(ctx, operator.Token, operator.CSRF, task.ID, "failed", "checked", testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("operator resolved: %v", err)
	}
	resolved, err := s.ResolveTask(ctx, owner.Token, owner.CSRF, task.ID, "failed", "collector was not running on the host", testTime)
	if err != nil || resolved.State != "failed" {
		t.Fatalf("resolve: %+v %v", resolved, err)
	}
	var resolver, details string
	if err := s.db.QueryRow("SELECT resolved_by FROM tasks WHERE id=?", task.ID).Scan(&resolver); err != nil || resolver != owner.UserID {
		t.Fatalf("resolver: %q %v", resolver, err)
	}
	if err := s.db.QueryRow("SELECT details FROM audit_events WHERE action='task.resolve' AND outcome='success'").Scan(&details); err != nil {
		t.Fatal(err)
	}
	if want := `"evidence":"collector was not running on the host"`; !strings.Contains(details, want) {
		t.Fatalf("resolution evidence missing from audit: %s", details)
	}
}

func TestTaskAuditFailureRollsBack(t *testing.T) {
	s, owner, host := taskFixture(t)
	if _, err := s.db.Exec("CREATE TRIGGER fail_task_audit BEFORE INSERT ON audit_events WHEN NEW.action='task.create' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := newTask(t, s, owner, host.ID, "rollback-key-00001", testTime); err == nil {
		t.Fatal("task created without its audit event")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM tasks").Scan(&count); err != nil || count != 0 {
		t.Fatalf("task kept after audit failure: %d %v", count, err)
	}
}

func TestTasksSurviveRestartAndRestoreHoldsOpenWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "amuos.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	token, err := s.PrepareBootstrap(ctx, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Setup(ctx, token, "owner", testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	owner, err := s.Login(ctx, "owner", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "restart-host", testTime)
	if err != nil {
		t.Fatal(err)
	}
	task, err := newTask(t, s, owner, host.ID, "restart-key-000001", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RecoverTasks(ctx, testTime); err != nil {
		t.Fatal(err)
	}
	// A queued task was never handed out, so a restart leaves it queued.
	if state, _ := taskState(t, s, task.ID); state != "queued" {
		t.Fatalf("queued task after restart: %s", state)
	}
	if again, err := newTask(t, s, owner, host.ID, "restart-key-000001", testTime); err != nil || again.ID != task.ID {
		t.Fatalf("replay after restart: %v", err)
	}
	// A restored database may be older than the hosts' real state.
	if err := s.PrepareRestoredState(ctx, "restore-test", testTime, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if state, reason := taskState(t, s, task.ID); state != "unknown" || reason != "restored" {
		t.Fatalf("restored task: %s %s", state, reason)
	}
}
