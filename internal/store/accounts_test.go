package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

func inviteUser(t *testing.T, s *Store, issuer Session, name string, grants ...rbac.Grant) (User, Session) {
	t.Helper()
	ctx := context.Background()
	inv, err := s.CreateInvitation(ctx, issuer.Token, issuer.CSRF, name, grants, testTime)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.AcceptInvitation(ctx, inv.Token, testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Login(ctx, name, testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	return u, session
}

func TestInvitationsFreezeScopeAndProtectSecrets(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	a, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "a", testTime)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "b", testTime)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.CreateInvitation(ctx, owner.Token, owner.CSRF, " Member ", []rbac.Grant{{Role: rbac.Viewer, HostID: a.ID}}, testTime)
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err = s.db.QueryRow("SELECT token_hash FROM invitations WHERE id=?", inv.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 32 || string(stored) == inv.Token {
		t.Fatal("plaintext invitation stored")
	}
	u, err := s.AcceptInvitation(ctx, inv.Token, testPassword, testTime)
	if err != nil || u.Username != "member" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err = s.AcceptInvitation(ctx, inv.Token, testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("reused invitation: %v", err)
	}
	member, err := s.Login(ctx, "member", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := s.Hosts(ctx, member.Token, testTime)
	if err != nil || len(hosts) != 1 || hosts[0].ID != a.ID {
		t.Fatalf("scope %v %v", hosts, err)
	}
	p, err := s.Identity(ctx, member.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if p.Allows(rbac.HostRead, b.ID) || p.Allows(rbac.HostManage, a.ID) {
		t.Fatal("invite escalated scope")
	}
	if _, err = s.Users(ctx, member.Token, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer lists users: %v", err)
	}
	if _, err = s.CreateInvitation(ctx, member.Token, member.CSRF, "forbidden", []rbac.Grant{{Role: rbac.Viewer, All: true}}, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer invites: %v", err)
	}
	events, err := s.Events(ctx, owner.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(events)
	for _, secret := range []string{inv.Token, testPassword, owner.Token, owner.CSRF} {
		if strings.Contains(string(data), secret) {
			t.Fatal("secret leaked into audit")
		}
	}
}

func TestInvalidInvitationsAndReissue(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	grants := []rbac.Grant{{Role: rbac.Viewer, All: true}}
	for _, g := range []rbac.Grant{{Role: rbac.Owner, All: true}, {Role: rbac.Admin}, {Role: rbac.Viewer, All: true, HostID: "x"}, {Role: rbac.Viewer, HostID: "missing"}} {
		if _, err := s.CreateInvitation(ctx, owner.Token, owner.CSRF, "invalid", []rbac.Grant{g}, testTime); !errors.Is(err, ErrDenied) {
			t.Fatalf("grant %+v: %v", g, err)
		}
	}
	old, err := s.CreateInvitation(ctx, owner.Token, owner.CSRF, "member", grants, testTime)
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.CreateInvitation(ctx, owner.Token, owner.CSRF, "member", grants, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, old.Token, testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("old token: %v", err)
	}
	if err = s.RevokeInvitation(ctx, owner.Token, owner.CSRF, current.ID, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, current.Token, testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("revoked token: %v", err)
	}
	expired, err := s.CreateInvitation(ctx, owner.Token, owner.CSRF, "expired", grants, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, expired.Token, testPassword, testTime.Add(24*time.Hour)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("expiry boundary: %v", err)
	}
	if _, err = s.AcceptInvitation(ctx, "unknown", testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestInvitationIssuerStateIsRechecked(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	adminUser, admin := inviteUser(t, s, owner, "admin", rbac.Grant{Role: rbac.Admin, All: true})
	grants := []rbac.Grant{{Role: rbac.Viewer, All: true}}
	inv, err := s.CreateInvitation(ctx, admin.Token, admin.CSRF, "member", grants, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetUserEnabled(ctx, owner.Token, owner.CSRF, adminUser.ID, false, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.SetUserEnabled(ctx, owner.Token, owner.CSRF, adminUser.ID, true, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Identity(ctx, admin.Token, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("session resurrected: %v", err)
	}
	if _, err = s.AcceptInvitation(ctx, inv.Token, testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("issuer invitation resurrected: %v", err)
	}
	admin, err = s.Login(ctx, "admin", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	inv, err = s.CreateInvitation(ctx, admin.Token, admin.CSRF, "less-power", grants, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE grants SET role='Viewer' WHERE user_id=?", adminUser.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, inv.Token, testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("stale authority accepted: %v", err)
	}
}

func TestPasswordChangeInvalidatesEverySession(t *testing.T) {
	s, first, _ := prepared(t)
	ctx := context.Background()
	second, err := s.Login(ctx, "owner", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	const replacement = "a different secure password"
	if err = s.ChangePassword(ctx, first.Token, "wrong-csrf", testPassword, replacement, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("csrf %v", err)
	}
	if err = s.ChangePassword(ctx, first.Token, first.CSRF, "wrong-password", replacement, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("current password %v", err)
	}
	if _, err = s.Identity(ctx, second.Token, testTime); err != nil {
		t.Fatal("failed change invalidated session", err)
	}
	if err = s.ChangePassword(ctx, first.Token, first.CSRF, testPassword, replacement, testTime); err != nil {
		t.Fatal(err)
	}
	for _, session := range []Session{first, second} {
		if _, err = s.Identity(ctx, session.Token, testTime); !errors.Is(err, ErrCredentials) {
			t.Fatalf("session survived password change: %v", err)
		}
	}
	if _, err = s.Login(ctx, "owner", testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("old password accepted: %v", err)
	}
	if _, err = s.Login(ctx, "owner", replacement, testTime); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerProtectionAndReauthentication(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	_, admin := inviteUser(t, s, owner, "admin", rbac.Grant{Role: rbac.Admin, All: true})
	if err := s.SetUserEnabled(ctx, admin.Token, admin.CSRF, owner.UserID, false, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admin changed Owner: %v", err)
	}
	if err := s.SetUserEnabled(ctx, owner.Token, owner.CSRF, owner.UserID, false, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Owner without reauth: %v", err)
	}
	if err := s.Reauthenticate(ctx, owner.Token, owner.CSRF, "wrong", testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("bad reauth: %v", err)
	}
	if err := s.Reauthenticate(ctx, owner.Token, owner.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserEnabled(ctx, owner.Token, owner.CSRF, owner.UserID, false, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("last Owner disabled: %v", err)
	}
	if err := s.SetUserEnabled(ctx, owner.Token, owner.CSRF, owner.UserID, true, testTime.Add(5*time.Minute)); !errors.Is(err, ErrDenied) {
		t.Fatalf("stale reauth: %v", err)
	}
	other, err := s.Login(ctx, "owner", testPassword, testTime.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetUserEnabled(ctx, other.Token, other.CSRF, owner.UserID, true, testTime.Add(5*time.Minute)); !errors.Is(err, ErrDenied) {
		t.Fatalf("reauth shared across sessions: %v", err)
	}
}

func TestConcurrentOwnerDisableKeepsOneOwner(t *testing.T) {
	s, first, _ := prepared(t)
	ctx := context.Background()
	secondUser, second := inviteUser(t, s, first, "second", rbac.Grant{Role: rbac.Admin, All: true})
	// Owner grant management is not exposed yet. Seed the second Owner for the invariant test.
	if _, err := s.db.Exec("UPDATE grants SET role='Owner' WHERE user_id=?", secondUser.ID); err != nil {
		t.Fatal(err)
	}
	for _, session := range []Session{first, second} {
		if err := s.Reauthenticate(ctx, session.Token, session.CSRF, testPassword, testTime); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, session := range []Session{first, second} {
		wg.Add(1)
		go func(session Session) {
			defer wg.Done()
			results <- s.SetUserEnabled(ctx, session.Token, session.CSRF, session.UserID, false, testTime)
		}(session)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", successes, conflicts)
	}
	var enabled int
	if err := s.db.QueryRow("SELECT count(*) FROM users WHERE enabled=1").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("enabled=%d err=%v", enabled, err)
	}
}

func TestInvitationConcurrentAcceptance(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	inv, err := s.CreateInvitation(ctx, owner.Token, owner.CSRF, "member", []rbac.Grant{{Role: rbac.Viewer, All: true}}, testTime)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.AcceptInvitation(ctx, inv.Token, testPassword, testTime)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrCredentials) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("invite accepted %d times", successes)
	}
}

func TestAccountChangesRollbackOnAuditFailure(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	user, member := inviteUser(t, s, owner, "member", rbac.Grant{Role: rbac.Viewer, All: true})
	inv, err := s.CreateInvitation(ctx, owner.Token, owner.CSRF, "pending", []rbac.Grant{{Role: rbac.Viewer, All: true}}, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("CREATE TRIGGER fail_account_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetUserEnabled(ctx, owner.Token, owner.CSRF, user.ID, false, testTime); err == nil {
		t.Fatal("disabled user without audit")
	}
	if _, err = s.Identity(ctx, member.Token, testTime); err != nil {
		t.Fatal("failed disable removed session", err)
	}
	if err = s.ChangePassword(ctx, member.Token, member.CSRF, testPassword, "another secure password", testTime); err == nil {
		t.Fatal("changed password without audit")
	}
	if _, err = s.AcceptInvitation(ctx, inv.Token, testPassword, testTime); err == nil {
		t.Fatal("accepted invite without audit")
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM users WHERE username='pending'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial signup %d %v", count, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_account_audit"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Login(ctx, "member", testPassword, testTime); err != nil {
		t.Fatal("password change did not roll back", err)
	}
	if _, err = s.AcceptInvitation(ctx, inv.Token, testPassword, testTime); err != nil {
		t.Fatal("invite consumption did not roll back", err)
	}
}

func TestPendingInvitationSurvivesRestart(t *testing.T) {
	s, owner, path := prepared(t)
	ctx := context.Background()
	inv, err := s.CreateInvitation(ctx, owner.Token, owner.CSRF, "member", []rbac.Grant{{Role: rbac.Viewer, All: true}}, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.AcceptInvitation(ctx, inv.Token, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
}

func TestDeletedHostAndScopedAdminCannotGrantAccess(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	host, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "a", testTime)
	if err != nil {
		t.Fatal(err)
	}
	_, scoped := inviteUser(t, s, owner, "scoped-admin", rbac.Grant{Role: rbac.Admin, HostID: host.ID})
	if _, err = s.CreateInvitation(ctx, scoped.Token, scoped.CSRF, "invalid", []rbac.Grant{{Role: rbac.Viewer, HostID: host.ID}}, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("scoped Admin invited user: %v", err)
	}
	inv, err := s.CreateInvitation(ctx, owner.Token, owner.CSRF, "member", []rbac.Grant{{Role: rbac.Viewer, HostID: host.ID}}, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE hosts SET state='deleted' WHERE id=?", host.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, inv.Token, testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("deleted host granted: %v", err)
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM users WHERE username='member'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial user %d %v", count, err)
	}
}

func legacyDatabase(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := migrations.ReadFile("migrations/001_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(contents)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	return path, db
}

func TestMigrationPreservesExistingSession(t *testing.T) {
	path, legacy := legacyDatabase(t)
	hash, err := security.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("INSERT INTO users VALUES('old-owner','old-owner',?,1)", hash); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("INSERT INTO grants(id,user_id,role,all_hosts) VALUES('old-grant','old-owner','Owner',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?)", security.Digest("old-token"), security.Digest("old-csrf"), "old-owner", testTime.Unix(), testTime.Unix(), testTime.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.Identity(context.Background(), "old-token", testTime)
	if err != nil || p.ID != "old-owner" {
		t.Fatalf("lost legacy identity: %v %v", p, err)
	}
	if err = s.SetUserEnabled(context.Background(), "old-token", "old-csrf", "old-owner", true, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("migration granted reauth: %v", err)
	}
	var version int
	if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("version %d %v", version, err)
	}
}

func TestFailedMigrationRollsBackSchema(t *testing.T) {
	path, legacy := legacyDatabase(t)
	if _, err := legacy.Exec("CREATE TABLE invitations(id TEXT)"); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("conflicting migration succeeded")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, columns int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('sessions') WHERE name='reauthenticated_at'").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if version != 1 || columns != 0 {
		t.Fatalf("partial migration version=%d new_columns=%d", version, columns)
	}
}
