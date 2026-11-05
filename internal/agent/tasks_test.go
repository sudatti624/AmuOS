package agent

import (
	"context"
	"github.com/sudatti624/AmuOS/internal/store"
	"os"
	"testing"
	"time"
)

func TestCollectTaskAndCrashJournalNeverReexecute(t *testing.T) {
	cp := startControlPlane(t)
	host, code := cp.code(t)
	ctx := context.Background()
	directory := t.TempDir()
	a := cp.agent(t, directory)
	if _, err := a.Enroll(ctx, code); err != nil {
		t.Fatal(err)
	}
	a.cfg.Collector = &fakeCollector{}
	a.supported = true
	task, err := cp.db.CreateTask(store.WithIdempotencyKey(ctx, "collect-agent-test-0001"), cp.owner.Token, cp.owner.CSRF, host.ID, "collect", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.syncTasks(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := cp.db.Task(ctx, cp.owner.Token, task.ID, time.Now())
	if err != nil || result.State != "succeeded" {
		t.Fatalf("result %+v %v", result, err)
	}
	restarted := cp.agent(t, directory)
	restarted.cfg.Collector = &fakeCollector{}
	restarted.supported = true
	if err := restarted.syncTasks(ctx); err != nil {
		t.Fatal(err)
	}
	task, err = cp.db.CreateTask(store.WithIdempotencyKey(ctx, "collect-agent-test-0002"), cp.owner.Token, cp.owner.CSRF, host.ID, "collect", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, leaf, id, err := a.loadCredential()
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := cp.db.PollTasks(ctx, leaf.SerialNumber.Text(16), id.AgentID, nil, time.Now())
	// The canonical serial may have leading zero padding; get it from the
	// same certificate parser used by the HTTP middleware.
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.path("tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.saveTask(localTask{Delivery: *delivery}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.syncTasks(ctx); err != nil {
		t.Fatal(err)
	}
	result, err = cp.db.Task(ctx, cp.owner.Token, task.ID, time.Now())
	if err != nil || result.State != "unknown" {
		t.Fatalf("crash outcome %+v %v", result, err)
	}
}
