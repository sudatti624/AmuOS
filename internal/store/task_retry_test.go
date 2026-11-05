package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
)

func retryTask(s *Store, session Session, id, key string, now time.Time) (Task, error) {
	return s.RetryTask(WithIdempotencyKey(context.Background(), key), session.Token, session.CSRF, id, now)
}

// finish ends a queued task the way maintenance does when no Agent takes it.
func finish(t *testing.T, s *Store, id, state string, now time.Time) {
	t.Helper()
	var finished any = now.Unix()
	if state == "unknown" {
		finished = nil
	}
	if _, err := s.db.Exec("UPDATE tasks SET state=?,finished_at=? WHERE id=?", state, finished, id); err != nil {
		t.Fatal(err)
	}
}

func TestRetryCreatesALinkedTaskAndKeepsTheOriginal(t *testing.T) {
	s, owner, path := prepared(t)
	ctx := context.Background()
	host, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "retry-host", testTime)
	if err != nil {
		t.Fatal(err)
	}
	original, err := newTask(t, s, owner, host.ID, "retry-original-0001", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retryTask(s, owner, original.ID, "retry-while-queued-1", testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("queued task re-run: %v", err)
	}
	// An unknown result may be re-run, and stays unknown until someone resolves it.
	finish(t, s, original.ID, "unknown", testTime)
	retry, err := retryTask(s, owner, original.ID, "retry-first-000001", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID == original.ID || retry.RetryOf == nil || *retry.RetryOf != original.ID || retry.State != "queued" || retry.HostID != host.ID {
		t.Fatalf("retry: %+v", retry)
	}
	if state, _ := taskState(t, s, original.ID); state != "unknown" {
		t.Fatalf("original became %s", state)
	}
	// A lost response is recovered with the same key, not a second retry.
	again, err := retryTask(s, owner, original.ID, "retry-first-000001", testTime)
	if err != nil || again.ID != retry.ID || !again.Replayed {
		t.Fatalf("replay: %+v %v", again, err)
	}
	if _, err := retryTask(s, owner, original.ID, "retry-second-key-01", testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("second retry of the same task: %v", err)
	}
	if _, err := newTask(t, s, owner, host.ID, "retry-first-000001", testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry key reused for a plain task: %v", err)
	}

	got, err := s.Task(ctx, owner.Token, original.ID, testTime)
	if err != nil || got.RetriedBy == nil || *got.RetriedBy != retry.ID {
		t.Fatalf("original does not point to its retry: %+v %v", got, err)
	}
	var details string
	if err := s.db.QueryRow("SELECT details FROM audit_events WHERE action='task.retry' AND outcome='success'").Scan(&details); err != nil || !strings.Contains(details, original.ID) {
		t.Fatalf("audit: %q %v", details, err)
	}

	// The link survives a restart and the original's retention.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	finish(t, reopened, original.ID, "failed", testTime)
	if err := reopened.RunTaskMaintenance(ctx, testTime.Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	session := loginAt(t, reopened, testTime.Add(31*24*time.Hour))
	if _, err := reopened.Task(ctx, session.Token, original.ID, testTime.Add(31*24*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired original still present: %v", err)
	}
	var kept string
	if err := reopened.db.QueryRow("SELECT retry_of FROM tasks WHERE id=?", retry.ID).Scan(&kept); err != nil || kept != original.ID {
		t.Fatalf("reference lost: %q %v", kept, err)
	}
}

func TestRetryChainsAreLimitedAndScoped(t *testing.T) {
	s, owner, host := taskFixture(t)
	ctx := context.Background()
	task, err := newTask(t, s, owner, host.ID, "chain-original-0001", testTime)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= taskRetryLimit; i++ {
		finish(t, s, task.ID, "timed_out", testTime)
		if task, err = retryTask(s, owner, task.ID, "chain-retry-00000"+string(rune('0'+i)), testTime); err != nil {
			t.Fatalf("retry %d: %v", i, err)
		}
	}
	finish(t, s, task.ID, "failed", testTime)
	if _, err := retryTask(s, owner, task.ID, "chain-retry-beyond-1", testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry beyond the limit: %v", err)
	}
	var reason string
	if err := s.db.QueryRow("SELECT reason FROM audit_events WHERE action='task.retry' AND outcome='denied' ORDER BY id DESC LIMIT 1").Scan(&reason); err != nil || reason != "retry_limit" {
		t.Fatalf("denial reason %q %v", reason, err)
	}

	other, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "other-host", testTime)
	if err != nil {
		t.Fatal(err)
	}
	_, viewer := inviteUser(t, s, owner, "viewer", rbac.Grant{Role: rbac.Viewer, HostID: host.ID})
	_, operator := inviteUser(t, s, owner, "operator", rbac.Grant{Role: rbac.Operator, HostID: other.ID})
	fresh, err := newTask(t, s, owner, host.ID, "scope-original-0001", testTime)
	if err != nil {
		t.Fatal(err)
	}
	finish(t, s, fresh.ID, "failed", testTime)
	if _, err := retryTask(s, viewer, fresh.ID, "viewer-retry-000001", testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer re-ran a task: %v", err)
	}
	for _, id := range []string{fresh.ID, "missing-task"} {
		if _, err := retryTask(s, operator, id, "operator-retry-0001", testTime); !errors.Is(err, ErrNotFound) {
			t.Fatalf("task outside scope: %v", err)
		}
	}
	bad := owner
	bad.CSRF = "wrong"
	if _, err := retryTask(s, bad, fresh.ID, "owner-retry-csrf-01", testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("missing CSRF: %v", err)
	}
	if _, err := s.RetryTask(ctx, owner.Token, owner.CSRF, fresh.ID, testTime); !errors.Is(err, ErrInput) {
		t.Fatalf("missing idempotency key: %v", err)
	}
	if err := s.RevokeHost(ctx, owner.Token, owner.CSRF, host.ID, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := retryTask(s, owner, fresh.ID, "revoked-retry-00001", testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry on a revoked host: %v", err)
	}
}

func TestHostTasksListsOnlyThatHost(t *testing.T) {
	s, owner, host := taskFixture(t)
	ctx := context.Background()
	other, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "other-host", testTime)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := newTask(t, s, owner, host.ID, "host-list-mine-0001", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newTask(t, s, owner, other.ID, "host-list-other-001", testTime); err != nil {
		t.Fatal(err)
	}
	listed, err := s.HostTasks(ctx, owner.Token, host.ID, testTime)
	if err != nil || len(listed) != 1 || listed[0].ID != mine.ID {
		t.Fatalf("host tasks: %+v %v", listed, err)
	}
	_, viewer := inviteUser(t, s, owner, "viewer", rbac.Grant{Role: rbac.Viewer, HostID: host.ID})
	if listed, err := s.HostTasks(ctx, viewer.Token, host.ID, testTime); err != nil || len(listed) != 1 {
		t.Fatalf("viewer in scope: %+v %v", listed, err)
	}
	for _, id := range []string{other.ID, "missing-host"} {
		if _, err := s.HostTasks(ctx, viewer.Token, id, testTime); !errors.Is(err, ErrNotFound) {
			t.Fatalf("host outside scope: %v", err)
		}
	}
}
