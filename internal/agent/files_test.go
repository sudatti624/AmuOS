package agent

import (
	"bytes"
	"context"
	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/fileops"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileTaskMTLSMaxTransferAndCrashNeverReexecutes(t *testing.T) {
	cp := startControlPlane(t)
	host, code := cp.code(t)
	ctx := context.Background()
	a := cp.agent(t, t.TempDir())
	dir := t.TempDir()
	engine, err := fileops.New(map[string]fileops.RootConfig{"work": {Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	a.files = engine
	if _, err := a.Enroll(ctx, code); err != nil {
		t.Fatal(err)
	}
	if err := a.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cp.db.SetFileRoots(ctx, cp.owner.Token, cp.owner.CSRF, host.ID, []store.FileRoot{{Alias: "work", ReadRoles: []rbac.Role{rbac.Admin}, WriteRoles: []rbac.Role{rbac.Admin}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	create := func(key string, q fileops.Request) store.Task {
		t.Helper()
		q.Root = "work"
		task, err := cp.db.CreateFileTask(store.WithIdempotencyKey(ctx, key), cp.owner.Token, cp.owner.CSRF, host.ID, q, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	data := bytes.Repeat([]byte("x"), fileops.MaxFile)
	task := create("file-agent-upload-00001", fileops.Request{Action: "create", Path: "upload", Data: data})
	if err := a.syncTasks(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "upload"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("upload length %d %v", len(got), err)
	}
	outcome, err := cp.db.Task(ctx, cp.owner.Token, task.ID, time.Now())
	if err != nil || outcome.State != "succeeded" {
		t.Fatal(outcome, err)
	}
	task = create("file-agent-download-001", fileops.Request{Action: "read", Path: "upload"})
	if err := a.syncTasks(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := cp.db.FileTaskResult(ctx, cp.owner.Token, task.ID, time.Now())
	if err != nil || !bytes.Equal(result.Data, data) {
		t.Fatal("download", err)
	}
	task = create("file-agent-crash-000001", fileops.Request{Action: "create", Path: "must-not-execute"})
	_, leaf, id, err := a.loadCredential()
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := cp.db.PollTasks(ctx, agentca.SerialString(leaf.SerialNumber), id.AgentID, nil, time.Now())
	if err != nil || delivery == nil {
		t.Fatal(delivery, err)
	}
	if err := a.saveTask(localTask{Delivery: *delivery}); err != nil {
		t.Fatal(err)
	}
	restarted := cp.agent(t, a.cfg.StateDir)
	restarted.files = engine
	if err := restarted.syncTasks(ctx); err != nil {
		t.Fatal(err)
	}
	outcome, err = cp.db.Task(ctx, cp.owner.Token, task.ID, time.Now())
	if err != nil || outcome.State != "unknown" {
		t.Fatal(outcome, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "must-not-execute")); !os.IsNotExist(err) {
		t.Fatal("crashed operation reexecuted")
	}
}
