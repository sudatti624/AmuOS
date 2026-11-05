package store

import (
	"context"
	"errors"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"testing"
)

func TestMonitoringPolicyAuthorizationRestartAndAudit(t *testing.T) {
	s, owner, path := prepared(t)
	ctx := context.Background()
	defaults, err := s.MonitoringPolicy(ctx, owner.Token, testTime)
	if err != nil || defaults != (MonitoringPolicy{15, 7, 30}) {
		t.Fatalf("defaults: %+v %v", defaults, err)
	}
	_, admin := inviteUser(t, s, owner, "admin", rbac.Grant{Role: rbac.Admin, All: true})
	_, viewer := inviteUser(t, s, owner, "viewer", rbac.Grant{Role: rbac.Viewer, All: true})
	settings := MonitoringPolicy{30, 14, 60}
	if err := s.SetMonitoringPolicy(ctx, viewer.Token, viewer.CSRF, settings, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer: %v", err)
	}
	if _, err := s.MonitoringPolicy(ctx, viewer.Token, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer read: %v", err)
	}
	if err := s.SetMonitoringPolicy(ctx, admin.Token, "wrong", settings, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("csrf: %v", err)
	}
	for _, invalid := range []MonitoringPolicy{{4, 7, 30}, {301, 7, 30}, {15, 0, 30}, {15, 31, 60}, {15, 7, 401}, {15, 30, 7}} {
		if err := s.SetMonitoringPolicy(ctx, admin.Token, admin.CSRF, invalid, testTime); !errors.Is(err, ErrInput) {
			t.Fatalf("invalid %+v: %v", invalid, err)
		}
	}
	if err := s.SetMonitoringPolicy(ctx, admin.Token, admin.CSRF, settings, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER reject_policy_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'blocked'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMonitoringPolicy(ctx, admin.Token, admin.CSRF, defaults, testTime); err == nil {
		t.Fatal("accepted without audit")
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_policy_audit"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, err := reopened.MonitoringPolicy(ctx, owner.Token, testTime)
	if err != nil || actual != settings {
		t.Fatalf("persisted: %+v %v", actual, err)
	}
}
