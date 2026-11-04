package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

func seedAudit(t *testing.T, s *Store, at time.Time) int64 {
	t.Helper()
	result, err := s.db.Exec("INSERT INTO audit_events(at,actor_id,action,target_id,outcome,reason) VALUES(?,'fixture','fixture.old','','success','')", at.Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAuditRetentionAuthorizationAndRestart(t *testing.T) {
	s, owner, path := prepared(t)
	ctx := WithRequestID(context.Background(), security.Token())
	settings, err := s.AuditRetention(ctx, owner.Token, testTime)
	if err != nil || settings.Days != 180 {
		t.Fatalf("default: %v %v", settings, err)
	}
	_, admin := inviteUser(t, s, owner, "admin", rbac.Grant{Role: rbac.Admin, All: true})
	if _, err = s.AuditRetention(ctx, admin.Token, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.Reauthenticate(ctx, admin.Token, admin.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.SetAuditRetention(ctx, admin.Token, admin.CSRF, 30, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admin changed retention: %v", err)
	}
	if _, err = s.PurgeAudit(ctx, admin.Token, admin.CSRF, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admin purged audit: %v", err)
	}
	if err = s.SetAuditRetention(ctx, owner.Token, owner.CSRF, 30, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Owner skipped reauth: %v", err)
	}
	if err = s.Reauthenticate(ctx, owner.Token, owner.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	for _, days := range []int{0, -1, 3651} {
		if err = s.SetAuditRetention(ctx, owner.Token, owner.CSRF, days, testTime); !errors.Is(err, ErrInput) {
			t.Fatalf("days=%d err=%v", days, err)
		}
	}
	if err = s.SetAuditRetention(ctx, owner.Token, owner.CSRF, 30, testTime); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, owner.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	var details struct {
		Before int `json:"before_days"`
		After  int `json:"after_days"`
	}
	if err = json.Unmarshal(events[0].Details, &details); err != nil || details.Before != 180 || details.After != 30 || events[0].RequestID != RequestID(ctx) {
		t.Fatalf("configuration evidence: %s %v", events[0].Details, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	settings, err = reopened.AuditRetention(ctx, owner.Token, testTime)
	if err != nil || settings.Days != 30 {
		t.Fatalf("lost retention: %v %v", settings, err)
	}
}

func TestAuditPurgePreservesCutoffAndRecordsEvidence(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := WithRequestID(context.Background(), security.Token())
	cutoff := testTime.Add(-180 * 24 * time.Hour)
	expired := seedAudit(t, s, cutoff.Add(-time.Second))
	boundary := seedAudit(t, s, cutoff)
	if err := s.Reauthenticate(ctx, owner.Token, owner.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	result, err := s.PurgeAudit(ctx, owner.Token, owner.CSRF, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || result.Cutoff != cutoff.Unix() || result.FirstID != expired || result.LastID != expired || result.More {
		t.Fatalf("bad purge: %+v", result)
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM audit_events WHERE id=?", boundary).Scan(&count); err != nil || count != 1 {
		t.Fatalf("cutoff boundary deleted: %d %v", count, err)
	}
	events, err := s.Events(ctx, owner.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Action != "audit.retention.run" || events[0].RequestID != RequestID(ctx) {
		t.Fatal("missing purge audit")
	}
	var details AuditPurge
	if err = json.Unmarshal(events[0].Details, &details); err != nil || details.Deleted != 1 || details.FirstID != expired {
		t.Fatalf("bad purge evidence: %s %v", events[0].Details, err)
	}
	result, err = s.PurgeAudit(ctx, owner.Token, owner.CSRF, testTime)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("purged retained rows: %+v %v", result, err)
	}
}

func TestPurgeBatchesDoNotOverlapUnderConcurrency(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < auditRetentionBatch+2; i++ {
		if _, err = tx.Exec("INSERT INTO audit_events(at,actor_id,action,target_id,outcome,reason) VALUES(?,'fixture','fixture.old','','success','')", testTime.Add(-181*24*time.Hour).Unix()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.Reauthenticate(ctx, owner.Token, owner.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		Result AuditPurge
		Err    error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := s.PurgeAudit(ctx, owner.Token, owner.CSRF, testTime)
			results <- outcome{result, err}
		}()
	}
	wg.Wait()
	close(results)
	var batches []AuditPurge
	var total int64
	for outcome := range results {
		if outcome.Err != nil {
			t.Fatal(outcome.Err)
		}
		if outcome.Result.Deleted > auditRetentionBatch {
			t.Fatal("unbounded purge")
		}
		batches = append(batches, outcome.Result)
		total += outcome.Result.Deleted
	}
	if total != auditRetentionBatch+2 || !(batches[0].LastID < batches[1].FirstID || batches[1].LastID < batches[0].FirstID) {
		t.Fatalf("overlap or lost rows: %+v", batches)
	}
}

func TestRetentionAuditFailureRollsBackSettingsAndDeletion(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	old := seedAudit(t, s, testTime.Add(-181*24*time.Hour))
	if err := s.Reauthenticate(ctx, owner.Token, owner.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER fail_retention_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit failed'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAuditRetention(ctx, owner.Token, owner.CSRF, 1, testTime); err == nil {
		t.Fatal("changed policy without audit")
	}
	settings, err := s.AuditRetention(ctx, owner.Token, testTime)
	if err != nil || settings.Days != 180 {
		t.Fatalf("partial policy: %v %v", settings, err)
	}
	if _, err = s.PurgeAudit(ctx, owner.Token, owner.CSRF, testTime); err == nil {
		t.Fatal("deleted without audit")
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM audit_events WHERE id=?", old).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit row lost on failed purge: %d %v", count, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_retention_audit"); err != nil {
		t.Fatal(err)
	}
	result, err := s.PurgeAudit(ctx, owner.Token, owner.CSRF, testTime)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("recovery failed: %+v %v", result, err)
	}
}
