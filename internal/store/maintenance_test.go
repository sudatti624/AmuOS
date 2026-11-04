package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAutomaticAuditMaintenanceIsDurableAndNotDuplicated(t *testing.T) {
	s, owner, path := prepared(t)
	ctx := context.Background()
	old := seedAudit(t, s, testTime.Add(-181*24*time.Hour))
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); ran, err := s.RunAuditMaintenance(ctx, testTime); results <- ran; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	count := 0
	for ran := range results {
		if ran {
			count++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("maintenance ran %d times", count)
	}
	var existing int
	if err := s.db.QueryRow("SELECT count(*) FROM audit_events WHERE id=?", old).Scan(&existing); err != nil || existing != 0 {
		t.Fatalf("old row remains: %d %v", existing, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if ran, err := reopened.RunAuditMaintenance(ctx, testTime.Add(30*time.Minute)); err != nil || ran {
		t.Fatalf("schedule lost on restart: %v %v", ran, err)
	}
	settings, err := reopened.AuditRetention(ctx, owner.Token, testTime)
	if err != nil || settings.LastRun == nil || settings.NextRun != testTime.Add(time.Hour).Unix() {
		t.Fatalf("schedule: %+v %v", settings, err)
	}
}

func TestAutomaticMaintenanceCanBeDisabledByOwner(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	old := seedAudit(t, s, testTime.Add(-181*24*time.Hour))
	if err := s.Reauthenticate(ctx, owner.Token, owner.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	enabled := false
	if err := s.SetAuditPolicy(ctx, owner.Token, owner.CSRF, 180, &enabled, testTime); err != nil {
		t.Fatal(err)
	}
	if ran, err := s.RunAuditMaintenance(ctx, testTime); err != nil || ran {
		t.Fatal(ran, err)
	}
	var existing int
	if err := s.db.QueryRow("SELECT count(*) FROM audit_events WHERE id=?", old).Scan(&existing); err != nil || existing != 1 {
		t.Fatal(existing, err)
	}
	enabled = true
	if err := s.SetAuditPolicy(ctx, owner.Token, owner.CSRF, 180, &enabled, testTime); err != nil {
		t.Fatal(err)
	}
	if ran, err := s.RunAuditMaintenance(ctx, testTime); err != nil || !ran {
		t.Fatal(ran, err)
	}
}

func TestAutomaticMaintenanceFailureRollsBackAndRetries(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	old := seedAudit(t, s, testTime.Add(-181*24*time.Hour))
	if _, err := s.db.Exec("CREATE TRIGGER fail_auto_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'sensitive internal error'); END"); err != nil {
		t.Fatal(err)
	}
	if ran, err := s.RunAuditMaintenance(ctx, testTime); err == nil || ran {
		t.Fatal("failed audit committed automatic deletion")
	}
	var existing int
	if err := s.db.QueryRow("SELECT count(*) FROM audit_events WHERE id=?", old).Scan(&existing); err != nil || existing != 1 {
		t.Fatal(existing, err)
	}
	settings, err := s.AuditRetention(ctx, owner.Token, testTime)
	if err != nil || settings.Failures != 1 || settings.LastError != "maintenance_failed" || settings.LastRun != nil {
		t.Fatalf("failure status: %+v %v", settings, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_auto_audit"); err != nil {
		t.Fatal(err)
	}
	if ran, err := s.RunAuditMaintenance(ctx, testTime.Add(4*time.Minute)); err != nil || ran {
		t.Fatalf("backoff ignored: %v %v", ran, err)
	}
	if ran, err := s.RunAuditMaintenance(ctx, testTime.Add(5*time.Minute)); err != nil || !ran {
		t.Fatalf("retry failed: %v %v", ran, err)
	}
	settings, err = s.AuditRetention(ctx, owner.Token, testTime.Add(5*time.Minute))
	if err != nil || settings.Failures != 0 || settings.LastError != "" {
		t.Fatalf("did not recover: %+v %v", settings, err)
	}
}
