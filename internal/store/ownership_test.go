package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
)

func TestOwnerAddRemoveAndLastOwnerProtection(t *testing.T) {
	s, first, _ := prepared(t)
	ctx := context.Background()
	u, oldSession := inviteUser(t, s, first, "second", rbac.Grant{Role: rbac.Viewer, All: true})
	if err := s.SetOwnership(ctx, first.Token, first.CSRF, u.ID, true, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("promotion without reauth: %v", err)
	}
	if err := s.Reauthenticate(ctx, first.Token, first.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnership(ctx, first.Token, first.CSRF, u.ID, true, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Identity(ctx, oldSession.Token, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("promoted stale session survived: %v", err)
	}
	second, err := s.Login(ctx, "second", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Identity(ctx, second.Token, testTime)
	if err != nil || !owner(p) || p.Version <= 1 {
		t.Fatalf("missing promotion/version: %v %v", p, err)
	}
	if err = s.SetOwnership(ctx, first.Token, first.CSRF, first.UserID, false, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Identity(ctx, first.Token, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("demoted session survived: %v", err)
	}
	if err = s.Reauthenticate(ctx, second.Token, second.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.SetOwnership(ctx, second.Token, second.CSRF, u.ID, false, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("removed last Owner: %v", err)
	}
	direct, err := s.UserGrants(ctx, second.Token, u.ID, testTime)
	if err != nil || len(direct) != 2 {
		t.Fatalf("lost non-Owner grant: %v %v", direct, err)
	}
}

func TestOwnerChangesRejectUnauthorizedAndDisabledTargets(t *testing.T) {
	s, first, _ := prepared(t)
	ctx := context.Background()
	u, admin := inviteUser(t, s, first, "admin", rbac.Grant{Role: rbac.Admin, All: true})
	if err := s.Reauthenticate(ctx, admin.Token, admin.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnership(ctx, admin.Token, admin.CSRF, u.ID, true, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admin gained Owner: %v", err)
	}
	if err := s.TransferOwnership(ctx, admin.Token, admin.CSRF, u.ID, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if err := s.Reauthenticate(ctx, first.Token, first.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserEnabled(ctx, first.Token, first.CSRF, u.ID, false, testTime); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnership(ctx, first.Token, first.CSRF, u.ID, true, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("disabled Owner promotion: %v", err)
	}
	if err := s.TransferOwnership(ctx, first.Token, first.CSRF, u.ID, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("disabled transfer: %v", err)
	}
	if err := s.TransferOwnership(ctx, first.Token, first.CSRF, first.UserID, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("self transfer: %v", err)
	}
	if err := s.SetOwnership(ctx, first.Token, first.CSRF, "missing", true, testTime); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.SetOwnership(ctx, first.Token, first.CSRF, u.ID, true, testTime.Add(5*time.Minute)); !errors.Is(err, ErrDenied) {
		t.Fatalf("expired reauth: %v", err)
	}
}

func TestOwnershipTransferIsAtomicAndSurvivesRestart(t *testing.T) {
	s, first, path := prepared(t)
	ctx := context.Background()
	u, targetOld := inviteUser(t, s, first, "successor", rbac.Grant{Role: rbac.Viewer, All: true})
	inv, err := s.CreateInvitation(ctx, first.Token, first.CSRF, "pending", []rbac.Grant{{Role: rbac.Viewer, All: true}}, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Reauthenticate(ctx, first.Token, first.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.TransferOwnership(ctx, first.Token, first.CSRF, u.ID, testTime); err != nil {
		t.Fatal(err)
	}
	for _, session := range []Session{first, targetOld} {
		if _, err = s.Identity(ctx, session.Token, testTime); !errors.Is(err, ErrCredentials) {
			t.Fatalf("session survived transfer: %v", err)
		}
	}
	if _, err = s.AcceptInvitation(ctx, inv.Token, testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("old invitation survived: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retired, err := reopened.Login(ctx, "owner", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	p, err := reopened.Identity(ctx, retired.Token, testTime)
	if err != nil || owner(p) || !p.Allows(rbac.IdentityManage, "") {
		t.Fatalf("source did not become Admin: %v %v", p, err)
	}
	successor, err := reopened.Login(ctx, "successor", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	p, err = reopened.Identity(ctx, successor.Token, testTime)
	if err != nil || !owner(p) {
		t.Fatalf("target did not become Owner: %v %v", p, err)
	}
	if err = reopened.SetOwnership(ctx, successor.Token, successor.CSRF, u.ID, false, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("new login inherited reauth: %v", err)
	}
}

func TestOwnershipTransferRollsBackOnAuditFailure(t *testing.T) {
	s, first, _ := prepared(t)
	ctx := context.Background()
	u, second := inviteUser(t, s, first, "second", rbac.Grant{Role: rbac.Viewer, All: true})
	if err := s.Reauthenticate(ctx, first.Token, first.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER fail_owner_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit failed'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.TransferOwnership(ctx, first.Token, first.CSRF, u.ID, testTime); err == nil {
		t.Fatal("transfer succeeded without audit")
	}
	if err := s.SetOwnership(ctx, first.Token, first.CSRF, u.ID, true, testTime); err == nil {
		t.Fatal("promotion succeeded without audit")
	}
	for i, session := range []Session{first, second} {
		p, err := s.Identity(ctx, session.Token, testTime)
		if err != nil {
			t.Fatal("lost session on failed transfer", err)
		}
		if owner(p) != (i == 0) || p.Version != 1 {
			t.Fatalf("partial transfer: %+v", p)
		}
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_owner_audit"); err != nil {
		t.Fatal(err)
	}
	if err := s.TransferOwnership(ctx, first.Token, first.CSRF, u.ID, testTime); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentOwnerRemovalKeepsAnActiveOwner(t *testing.T) {
	s, first, _ := prepared(t)
	ctx := context.Background()
	u, _ := inviteUser(t, s, first, "second", rbac.Grant{Role: rbac.Viewer, All: true})
	if err := s.Reauthenticate(ctx, first.Token, first.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnership(ctx, first.Token, first.CSRF, u.ID, true, testTime); err != nil {
		t.Fatal(err)
	}
	second, err := s.Login(ctx, "second", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Reauthenticate(ctx, second.Token, second.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, session := range []Session{first, second} {
		wg.Add(1)
		go func(session Session) {
			defer wg.Done()
			results <- s.SetOwnership(ctx, session.Token, session.CSRF, session.UserID, false, testTime)
		}(session)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	var count int
	if err = s.db.QueryRow("SELECT count(DISTINCT u.id) FROM users u JOIN grants g ON g.user_id=u.id WHERE u.enabled=1 AND g.role='Owner'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("owners=%d err=%v", count, err)
	}
}
