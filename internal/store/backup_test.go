package store

import (
	"context"
	"errors"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestSnapshotIncludesWALAndValidatesExactSchema(t *testing.T) {
	s, session, _ := prepared(t)
	ctx := context.Background()
	if _, err := s.CreateHost(ctx, session.Token, session.CSRF, "wal-host", testTime); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot with # and spaces.sqlite")
	if err := s.Snapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	var count int
	if err := copy.db.QueryRow("SELECT count(*) FROM hosts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("WAL snapshot: %d %v", count, err)
	}
	if _, err := copy.db.Exec("CREATE TRIGGER sqliteEvil AFTER INSERT ON hosts BEGIN DELETE FROM users; END"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot(ctx, snapshot); err == nil {
		t.Fatal("accepted injected schema")
	}
}

func TestRestorePreparationAndMaintenanceAuditRollback(t *testing.T) {
	s, session, path := prepared(t)
	ctx := context.Background()
	invitation, err := s.CreateInvitation(ctx, session.Token, session.CSRF, "invited", []rbac.Grant{{Role: rbac.Viewer, All: true}}, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER reject_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'blocked'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.StartLocalMaintenance(ctx, "failed-start", "backup", testTime, nil); err == nil {
		t.Fatal("maintenance succeeded without audit")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM local_maintenance").Scan(&count); err != nil || count != 0 {
		t.Fatalf("non-atomic start: %d %v", count, err)
	}
	if err := s.PrepareRestoredState(ctx, "restore-id", testTime, nil); err == nil {
		t.Fatal("restore succeeded without audit")
	}
	if _, err := s.Identity(ctx, session.Token, testTime); err != nil {
		t.Fatal("audit failure revoked session")
	}
	var revoked any
	if err := s.db.QueryRow("SELECT revoked_at FROM invitations WHERE id=?", invitation.ID).Scan(&revoked); err != nil || revoked != nil {
		t.Fatalf("audit failure revoked invitation: %v %v", revoked, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_audit"); err != nil {
		t.Fatal(err)
	}
	if err := s.StartLocalMaintenance(ctx, "interrupted", "backup", testTime, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RecoverLocalMaintenance(ctx, testTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := reopened.RecoverLocalMaintenance(ctx, testTime.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := reopened.db.QueryRow("SELECT state FROM local_maintenance WHERE id='interrupted'").Scan(&state); err != nil || state != "unknown" {
		t.Fatalf("recovered state: %q %v", state, err)
	}
	if err := reopened.db.QueryRow("SELECT count(*) FROM audit_events WHERE action='backup.recover'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate recovery: %d %v", count, err)
	}
}

func TestIncompleteSQLiteRestoreRollsBack(t *testing.T) {
	ctx := context.Background()
	source, err := Open(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Force several backup pages so one step cannot complete the transaction.
	if _, err := source.db.Exec("WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000) INSERT INTO hosts(id,name) SELECT 'new-'||x,hex(randomblob(1000)) FROM n"); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "source.sqlite")
	if err := source.Snapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	destination, _, _ := prepared(t)
	if _, err := destination.db.Exec("INSERT INTO hosts(id,name) VALUES('original','original')"); err != nil {
		t.Fatal(err)
	}
	conn, err := destination.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Raw(func(raw any) error {
		backup, err := raw.(interface {
			NewRestore(string) (*sqlite.Backup, error)
		}).NewRestore(snapshot)
		if err != nil {
			return err
		}
		more, err := backup.Step(1)
		if err != nil {
			_ = backup.Finish()
			return err
		}
		if !more {
			_ = backup.Finish()
			t.Fatal("test restore finished too early")
		}
		return backup.Finish() // Abandon the unfinished transaction.
	})
	_ = conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := destination.db.QueryRow("SELECT count(*) FROM hosts WHERE id='original'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial restore changed original: %d %v", count, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := destination.ReplaceFrom(canceled, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore: %v", err)
	}
}
