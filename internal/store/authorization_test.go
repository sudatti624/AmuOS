package store

import (
	"context"
	"errors"
	"testing"

	"github.com/sudatti624/AmuOS/internal/rbac"
)

func TestGroupAndDirectGrantsKeepScopesSeparate(t *testing.T) {
	s, admin, _ := prepared(t)
	ctx := context.Background()
	a, err := s.CreateHost(ctx, admin.Token, admin.CSRF, "a", testTime)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateHost(ctx, admin.Token, admin.CSRF, "b", testTime)
	if err != nil {
		t.Fatal(err)
	}
	u, member := inviteUser(t, s, admin, "member", rbac.Grant{Role: rbac.Viewer, HostID: a.ID})
	g, err := s.CreateGroup(ctx, admin.Token, admin.CSRF, " Operations ", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "operations" {
		t.Fatal(g)
	}
	if err = s.SetGroupGrants(ctx, admin.Token, admin.CSRF, g.ID, []rbac.Grant{{Role: rbac.Admin, HostID: b.ID}}, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, admin.Token, admin.CSRF, g.ID, []string{u.ID}, testTime); err != nil {
		t.Fatal(err)
	}
	p, err := s.Identity(ctx, member.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version <= 1 || !p.Allows(rbac.HostManage, b.ID) || p.Allows(rbac.HostManage, a.ID) {
		t.Fatalf("scope mixed: %+v", p)
	}
	if err = s.SetGroupGrants(ctx, admin.Token, admin.CSRF, g.ID, []rbac.Grant{}, testTime); err != nil {
		t.Fatal(err)
	}
	after, err := s.Identity(ctx, member.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version <= p.Version || after.Allows(rbac.HostRead, b.ID) || !after.Allows(rbac.HostRead, a.ID) {
		t.Fatalf("stale group permissions: %+v", after)
	}
	if err = s.SetUserGrants(ctx, admin.Token, admin.CSRF, u.ID, []rbac.Grant{{Role: rbac.Operator, HostID: b.ID}}, testTime); err != nil {
		t.Fatal(err)
	}
	direct, err := s.UserGrants(ctx, admin.Token, u.ID, testTime)
	if err != nil || len(direct) != 1 || direct[0].Role != rbac.Operator {
		t.Fatalf("direct=%v err=%v", direct, err)
	}
	p, err = s.Identity(ctx, member.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Allows(rbac.Collect, b.ID) || p.Allows(rbac.HostRead, a.ID) {
		t.Fatal("direct change not reflected")
	}
}

func TestGroupOwnerAndDelegationGuards(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	_, admin := inviteUser(t, s, owner, "admin", rbac.Grant{Role: rbac.Admin, All: true})
	_, viewer := inviteUser(t, s, owner, "viewer", rbac.Grant{Role: rbac.Viewer, All: true})
	g, err := s.CreateGroup(ctx, admin.Token, admin.CSRF, "team", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateGroup(ctx, viewer.Token, viewer.CSRF, "forbidden", testTime); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if err = s.SetGroupGrants(ctx, admin.Token, admin.CSRF, g.ID, []rbac.Grant{{Role: rbac.Owner, All: true}}, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("group Owner allowed: %v", err)
	}
	if err = s.SetUserGrants(ctx, admin.Token, admin.CSRF, owner.UserID, []rbac.Grant{}, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admin removed Owner grants: %v", err)
	}
	if err = s.SetGroupMembers(ctx, admin.Token, admin.CSRF, g.ID, []string{owner.UserID}, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admin altered Owner membership: %v", err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{owner.UserID}, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Owner skipped reauth: %v", err)
	}
	if err = s.Reauthenticate(ctx, owner.Token, owner.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{owner.UserID}, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteGroup(ctx, admin.Token, admin.CSRF, g.ID, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admin deleted Owner group: %v", err)
	}
	if err = s.SetGroupGrants(ctx, admin.Token, admin.CSRF, g.ID, []rbac.Grant{{Role: rbac.Viewer, All: true}}, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admin changed Owner group: %v", err)
	}
	if err = s.DeleteGroup(ctx, owner.Token, owner.CSRF, g.ID, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("reauth survived permission change: %v", err)
	}
	if err = s.Reauthenticate(ctx, owner.Token, owner.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteGroup(ctx, owner.Token, owner.CSRF, g.ID, testTime); err != nil {
		t.Fatal(err)
	}
}

func TestGroupInputsAndMembershipRemoval(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	u, _ := inviteUser(t, s, owner, "member", rbac.Grant{Role: rbac.Viewer, All: true})
	g, err := s.CreateGroup(ctx, owner.Token, owner.CSRF, "team", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateGroup(ctx, owner.Token, owner.CSRF, "TEAM", testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, nil, testTime); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{u.ID, u.ID}, testTime); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{g.ID}, testTime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nested group: %v", err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{u.ID}, testTime); err != nil {
		t.Fatal(err)
	}
	host, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "retired", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupGrants(ctx, owner.Token, owner.CSRF, g.ID, []rbac.Grant{{Role: rbac.Viewer, HostID: host.ID}}, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE hosts SET state='deleted' WHERE id=?", host.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{}, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.SetUserGrants(ctx, owner.Token, owner.CSRF, u.ID, []rbac.Grant{{Role: rbac.Owner, All: true}}, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("Owner through generic replacement: %v", err)
	}
	if err = s.SetGroupGrants(ctx, owner.Token, owner.CSRF, g.ID, []rbac.Grant{{Role: rbac.Viewer, All: true}, {Role: rbac.Viewer, All: true}}, testTime); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}

func TestPermissionChangeRevokesOutstandingInvitations(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	u, member := inviteUser(t, s, owner, "member", rbac.Grant{Role: rbac.Viewer, All: true})
	g, err := s.CreateGroup(ctx, owner.Token, owner.CSRF, "admins", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{u.ID}, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupGrants(ctx, owner.Token, owner.CSRF, g.ID, []rbac.Grant{{Role: rbac.Admin, All: true}}, testTime); err != nil {
		t.Fatal(err)
	}
	inv, err := s.CreateInvitation(ctx, member.Token, member.CSRF, "child", []rbac.Grant{{Role: rbac.Admin, All: true}}, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupGrants(ctx, owner.Token, owner.CSRF, g.ID, []rbac.Grant{}, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupGrants(ctx, owner.Token, owner.CSRF, g.ID, []rbac.Grant{{Role: rbac.Admin, All: true}}, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, inv.Token, testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("old invitation revived after regrant: %v", err)
	}
}

func TestAuthorizationChangesRollbackWithAudit(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	u, member := inviteUser(t, s, owner, "member", rbac.Grant{Role: rbac.Viewer, All: true})
	g, err := s.CreateGroup(ctx, owner.Token, owner.CSRF, "team", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{u.ID}, testTime); err != nil {
		t.Fatal(err)
	}
	before, err := s.Identity(ctx, member.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("CREATE TRIGGER deny_authz_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit failed'); END"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{}, testTime); err == nil {
		t.Fatal("membership changed without audit")
	}
	if err = s.SetUserGrants(ctx, owner.Token, owner.CSRF, u.ID, []rbac.Grant{}, testTime); err == nil {
		t.Fatal("grants changed without audit")
	}
	if err = s.DeleteGroup(ctx, owner.Token, owner.CSRF, g.ID, testTime); err == nil {
		t.Fatal("group deleted without audit")
	}
	after, err := s.Identity(ctx, member.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if before.Version != after.Version || len(after.Grants) != 1 {
		t.Fatal("partial authorization mutation")
	}
	groups, err := s.Groups(ctx, owner.Token, testTime)
	if err != nil || len(groups) != 1 || len(groups[0].Members) != 1 {
		t.Fatalf("partial group mutation: %v %v", groups, err)
	}
}

func TestGroupsSurviveRestartAndLoseAccessOnDelete(t *testing.T) {
	s, owner, path := prepared(t)
	ctx := context.Background()
	u, member := inviteUser(t, s, owner, "member", rbac.Grant{Role: rbac.Viewer, All: true})
	g, err := s.CreateGroup(ctx, owner.Token, owner.CSRF, "team", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{u.ID}, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupGrants(ctx, owner.Token, owner.CSRF, g.ID, []rbac.Grant{{Role: rbac.Admin, All: true}}, testTime); err != nil {
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
	p, err := reopened.Identity(ctx, member.Token, testTime)
	if err != nil || !p.Allows(rbac.IdentityManage, "") {
		t.Fatalf("lost group grant: %v %v", p, err)
	}
	if err = reopened.DeleteGroup(ctx, owner.Token, owner.CSRF, g.ID, testTime); err != nil {
		t.Fatal(err)
	}
	p, err = reopened.Identity(ctx, member.Token, testTime)
	if err != nil || p.Allows(rbac.IdentityManage, "") {
		t.Fatalf("stale deleted group grant: %v %v", p, err)
	}
}
