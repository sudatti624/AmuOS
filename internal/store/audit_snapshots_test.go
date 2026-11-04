package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sudatti624/AmuOS/internal/rbac"
)

func TestMutationSnapshotsDescribeChangesWithoutCredentials(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	u, member := inviteUser(t, s, owner, "member", rbac.Grant{Role: rbac.Viewer, All: true})
	if err := s.SetUserGrants(ctx, owner.Token, owner.CSRF, u.ID, []rbac.Grant{{Role: rbac.Operator, All: true}}, testTime); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, owner.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	var changed struct {
		Before struct {
			Direct  []rbac.Grant `json:"direct_grants"`
			Version int64        `json:"authorization_version"`
		}
		After struct {
			Direct  []rbac.Grant `json:"direct_grants"`
			Version int64        `json:"authorization_version"`
		}
	}
	if err = json.Unmarshal(events[0].Details, &changed); err != nil {
		t.Fatal(err)
	}
	if changed.Before.Direct[0].Role != rbac.Viewer || changed.After.Direct[0].Role != rbac.Operator || changed.After.Version <= changed.Before.Version {
		t.Fatalf("incorrect change summary: %s", events[0].Details)
	}
	const replacement = "replacement password for audit"
	if err = s.ChangePassword(ctx, member.Token, member.CSRF, testPassword, replacement, testTime); err != nil {
		t.Fatal(err)
	}
	events, err = s.Events(ctx, owner.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	var password struct {
		Before struct {
			User struct {
				Sessions int `json:"session_count"`
			}
		}
		After struct {
			User struct {
				Sessions int `json:"session_count"`
			}
		}
	}
	if err = json.Unmarshal(events[0].Details, &password); err != nil || password.Before.User.Sessions != 1 || password.After.User.Sessions != 0 {
		t.Fatalf("session summary: %s %v", events[0].Details, err)
	}
	encoded, _ := json.Marshal(events)
	for _, secret := range []string{testPassword, replacement, member.Token, member.CSRF, "password_hash", "token_hash", "csrf_hash"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("sensitive field in summary: %s", secret)
		}
	}
}

func TestGroupAndOwnershipSnapshots(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := context.Background()
	u, _ := inviteUser(t, s, owner, "member", rbac.Grant{Role: rbac.Viewer, All: true})
	g, err := s.CreateGroup(ctx, owner.Token, owner.CSRF, "team", testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetGroupMembers(ctx, owner.Token, owner.CSRF, g.ID, []string{u.ID}, testTime); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, owner.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	var group struct {
		Before Group
		After  Group
	}
	if err = json.Unmarshal(events[0].Details, &group); err != nil || len(group.Before.Members) != 0 || len(group.After.Members) != 1 {
		t.Fatalf("membership summary: %s %v", events[0].Details, err)
	}
	if err = s.Reauthenticate(ctx, owner.Token, owner.CSRF, testPassword, testTime); err != nil {
		t.Fatal(err)
	}
	if err = s.TransferOwnership(ctx, owner.Token, owner.CSRF, u.ID, testTime); err != nil {
		t.Fatal(err)
	}
	var details string
	if err = s.db.QueryRow("SELECT details FROM audit_events WHERE action='owner.transfer'").Scan(&details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, "source") || !strings.Contains(details, "destination") || !strings.Contains(details, "Admin") || !strings.Contains(details, "Owner") {
		t.Fatalf("transfer summary: %s", details)
	}
}
