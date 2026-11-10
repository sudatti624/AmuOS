package store

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/fileops"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"path/filepath"
	"testing"
	"time"
)

func TestFileMigrationPreservesV13TasksAndRollsBackFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			path, legacy := legacyDatabase(t)
			for i := 1; i < 13; i++ {
				data, err := migrations.ReadFile("migrations/" + migrationNames[i])
				if err != nil {
					t.Fatal(err)
				}
				if _, err := legacy.Exec(string(data)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := legacy.Exec("PRAGMA user_version=13"); err != nil {
				t.Fatal(err)
			}
			s := &Store{db: legacy}
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
			host, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "legacy-host", testTime)
			if err != nil {
				t.Fatal(err)
			}
			ca, err := agentca.Load(t.TempDir(), testTime)
			if err != nil {
				t.Fatal(err)
			}
			a := enrollAgent(t, s, owner, host, ca)
			task, err := newTask(t, s, owner, host.ID, "legacy-file-migration-key", testTime)
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.PollTasks(ctx, a.serial, a.credential.AgentID, nil, testTime)
			if err != nil || d == nil {
				t.Fatal(d, err)
			}
			if fail {
				if _, err := legacy.Exec("CREATE TABLE file_roots(conflict TEXT)"); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			migrated, err := Open(path)
			if fail {
				if err == nil {
					migrated.Close()
					t.Fatal("migration should fail")
				}
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var version, count int
				if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow("SELECT count(*) FROM task_attempts WHERE task_id=? AND attempt_id=?", task.ID, d.AttemptID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if version != 13 || count != 1 {
					t.Fatal("partial migration", version, count)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer migrated.Close()
			got, err := migrated.Task(ctx, owner.Token, task.ID, testTime)
			if err != nil || got.State != "dispatching" || got.Attempt != 1 {
				t.Fatal("lost task/session", got, err)
			}
			receipt := TaskReceipt{ID: d.ID, AttemptID: d.AttemptID, InputHash: d.InputHash}
			if _, err := migrated.PollTasks(ctx, a.serial, a.credential.AgentID, &receipt, testTime); err != nil {
				t.Fatal("lost attempt", err)
			}
			if err := migrated.RecordTaskResult(ctx, a.serial, a.credential.AgentID, TaskResult{TaskReceipt: receipt, Outcome: "succeeded"}, testTime); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestFileSnapshotRestorationHoldsPendingRequests(t *testing.T) {
	s, owner, host := taskFixture(t)
	ctx := context.Background()
	if err := s.SetFileRoots(ctx, owner.Token, owner.CSRF, host.ID, []FileRoot{{Alias: "work", ReadRoles: []rbac.Role{rbac.Admin}, WriteRoles: []rbac.Role{rbac.Admin}}}, testTime); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateFileTask(WithIdempotencyKey(ctx, "file-restore-key-000001"), owner.Token, owner.CSRF, host.ID, fileops.Request{Root: "work", Action: "create", Path: "pending", Data: []byte("pending-transfer")}, testTime)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
	if err := s.Snapshot(ctx, destination); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot(ctx, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.PrepareRestoredState(ctx, "file-restore", testTime.Add(time.Second), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Identity(ctx, owner.Token, testTime); err == nil {
		t.Fatal("old credential survived")
	}
	session, err := restored.Login(ctx, "owner", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	got, err := restored.Task(ctx, session.Token, task.ID, testTime)
	if err != nil || got.State != "unknown" {
		t.Fatal(got, err)
	}
	roots, err := restored.FileRoots(ctx, session.Token, host.ID, testTime)
	if err != nil || len(roots) != 1 {
		t.Fatal("lost policy", roots, err)
	}
	var content string
	if err := restored.db.QueryRow("SELECT request FROM file_tasks WHERE task_id=?", task.ID).Scan(&content); err != nil || content == "" {
		t.Fatal("lost transfer", err)
	}
}
