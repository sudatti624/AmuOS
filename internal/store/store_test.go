package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

var testTime = time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)

const testPassword = "correct horse battery staple"

func prepared(t *testing.T) (*Store, Session, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "amuos.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	token, err := s.PrepareBootstrap(context.Background(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Setup(context.Background(), token, "owner", testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	session, err := s.Login(context.Background(), "owner", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	return s, session, path
}

func TestBootstrapOneUseAndExpiry(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token, err := s.PrepareBootstrap(ctx, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Setup(ctx, token, "owner", testPassword, testTime.Add(10*time.Minute)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("expiry: %v", err)
	}
	if err = s.Setup(ctx, "wrong", "owner", testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("wrong token: %v", err)
	}
	if err = s.Setup(ctx, token, "owner", testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.Setup(ctx, token, "other", testPassword, testTime); err == nil {
		t.Fatal("setup token reused")
	}
	if _, err = s.PrepareBootstrap(ctx, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("setup reopened: %v", err)
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestProfileReturnsOnlyOwnUsername(t *testing.T) {
	s, session, path := prepared(t)
	ctx := context.Background()
	p, username, err := s.Profile(ctx, session.Token, testTime)
	if err != nil || username != "owner" || p.ID != session.UserID {
		t.Fatalf("profile=%v %q err=%v", p, username, err)
	}
	if _, username, err = s.Profile(ctx, "unknown", testTime); !errors.Is(err, ErrCredentials) || username != "" {
		t.Fatalf("unknown session: %q %v", username, err)
	}
	if _, _, err = s.Profile(ctx, session.Token, testTime.Add(30*time.Minute)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("idle expiry: %v", err)
	}
	s.Close()
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, username, err = reopened.Profile(ctx, session.Token, testTime); err != nil || username != "owner" {
		t.Fatalf("after restart: %q %v", username, err)
	}
}

func TestSessionExpiryAndLogout(t *testing.T) {
	s, session, _ := prepared(t)
	ctx := context.Background()
	if _, err := s.Identity(ctx, session.Token, testTime.Add(30*time.Minute)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("idle expiry: %v", err)
	}
	if err := s.Logout(ctx, session.Token, "wrong", testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("csrf: %v", err)
	}
	if _, err := s.Identity(ctx, session.Token, testTime); err != nil {
		t.Fatal("invalid CSRF destroyed session", err)
	}
	if err := s.Logout(ctx, session.Token, session.CSRF, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Identity(ctx, session.Token, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("logout: %v", err)
	}
	session, err := s.Login(ctx, "owner", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE sessions SET last_seen=?", testTime.Add(12*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Identity(ctx, session.Token, testTime.Add(12*time.Hour)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("absolute expiry: %v", err)
	}
}

func TestPersistenceAndDisabledUser(t *testing.T) {
	s, session, path := prepared(t)
	ctx := context.Background()
	host, err := s.CreateHost(ctx, session.Token, session.CSRF, "server-a", testTime)
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
	hosts, err := reopened.Hosts(ctx, session.Token, testTime)
	if err != nil || len(hosts) != 1 || hosts[0].ID != host.ID {
		t.Fatalf("hosts=%v err=%v", hosts, err)
	}
	events, err := reopened.Events(ctx, session.Token, testTime)
	if err != nil || len(events) < 4 {
		t.Fatalf("audit lost: %v %v", events, err)
	}
	for _, event := range events {
		if strings.Contains(event.Target, testPassword) || strings.Contains(event.Reason, session.Token) {
			t.Fatal("secret in audit")
		}
	}
	if _, err = reopened.db.Exec("UPDATE users SET enabled=0 WHERE id=?", session.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Hosts(ctx, session.Token, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("disabled user: %v", err)
	}
}

func TestScopedGroupAndLivePermissionChange(t *testing.T) {
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
	hash, err := security.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO users(id,username,password_hash) VALUES('member','member',?)", []any{hash}},
		{"INSERT INTO groups VALUES('team','team')", nil},
		{"INSERT INTO memberships VALUES('member','team')", nil},
		{"INSERT INTO grants(id,group_id,role,all_hosts,host_id) VALUES('grant-a','team','Admin',0,?)", []any{a.ID}},
		{"INSERT INTO grants(id,user_id,role,all_hosts,host_id) VALUES('grant-b','member','Viewer',0,?)", []any{b.ID}},
	} {
		if _, err = s.db.Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	member, err := s.Login(ctx, "member", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := s.Hosts(ctx, member.Token, testTime)
	if err != nil || len(hosts) != 2 {
		t.Fatalf("hosts=%v err=%v", hosts, err)
	}
	p, err := s.Identity(ctx, member.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Allows(rbac.HostManage, a.ID) || p.Allows(rbac.HostManage, b.ID) {
		t.Fatal("scope escalation")
	}
	if _, err = s.CreateHost(ctx, member.Token, member.CSRF, "forbidden", testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("new global host: %v", err)
	}
	events, err := s.Events(ctx, member.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].HostID != a.ID {
		t.Fatalf("scope leaked audit: %+v", events)
	}
	if _, err = s.db.Exec("DELETE FROM memberships WHERE user_id='member'"); err != nil {
		t.Fatal(err)
	}
	hosts, err = s.Hosts(ctx, member.Token, testTime)
	if err != nil || len(hosts) != 1 || hosts[0].ID != b.ID {
		t.Fatalf("stale scope: %v %v", hosts, err)
	}
	if _, err = s.Events(ctx, member.Token, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("viewer audit: %v", err)
	}
}

func TestAuditFailureRollsBackChange(t *testing.T) {
	s, session, _ := prepared(t)
	if _, err := s.db.Exec("CREATE TRIGGER fail_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHost(context.Background(), session.Token, session.CSRF, "must-rollback", testTime); err == nil {
		t.Fatal("mutation succeeded without audit")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM hosts").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial mutation: count=%d err=%v", count, err)
	}
}

func TestStorageConstraintsAndSecrets(t *testing.T) {
	s, session, _ := prepared(t)
	var tokenHash, csrfHash []byte
	if err := s.db.QueryRow("SELECT token_hash,csrf_hash FROM sessions").Scan(&tokenHash, &csrfHash); err != nil {
		t.Fatal(err)
	}
	if string(tokenHash) == session.Token || string(csrfHash) == session.CSRF || len(tokenHash) != 32 {
		t.Fatal("plaintext session persisted")
	}
	if _, err := s.db.Exec("INSERT INTO groups VALUES('team','team')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO grants(id,group_id,role,all_hosts) VALUES('invalid','team','Owner',1)"); err == nil {
		t.Fatal("group Owner accepted")
	}
}
