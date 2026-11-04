package rbac

import "testing"

func TestScopeComposition(t *testing.T) {
	p := Principal{Enabled: true, Grants: []Grant{{Role: Admin, HostID: "a"}, {Role: Viewer, HostID: "b"}}}
	tests := []struct {
		action Action
		host   string
		want   bool
	}{
		{HostRead, "a", true}, {HostManage, "a", true}, {HostRead, "b", true}, {HostManage, "b", false},
		{HostRead, "c", false}, {HostManage, "", false}, {Backup, "a", false}, {IdentityManage, "", false}, {AuditRead, "", false},
	}
	for _, tt := range tests {
		if got := p.Allows(tt.action, tt.host); got != tt.want {
			t.Errorf("%s %q: got %v want %v", tt.action, tt.host, got, tt.want)
		}
	}
	p.Enabled = false
	if p.Allows(HostRead, "a") {
		t.Fatal("disabled principal was authorized")
	}
}

func TestOwnerConstraints(t *testing.T) {
	for _, role := range []Role{Admin, Operator, Viewer} {
		p := Principal{Enabled: true, Grants: []Grant{{Role: role, All: true}}}
		if p.Allows(Backup, "") || p.Allows(OwnerManage, "") {
			t.Fatalf("%s received owner-only power", role)
		}
	}
	malformed := Principal{Enabled: true, Grants: []Grant{{Role: Owner, HostID: "a"}}}
	if malformed.Allows(Backup, "a") {
		t.Fatal("scoped Owner must not grant permissions")
	}
	owner := Principal{Enabled: true, Grants: []Grant{{Role: Owner, All: true}}}
	if !owner.Allows(Backup, "") || !owner.Allows(HostManage, "new-host") {
		t.Fatal("valid Owner denied")
	}
	if owner.Allows(Action("unknown.operation"), "") {
		t.Fatal("undefined action allowed")
	}
}

func TestDelegationPreservesOperationAndScope(t *testing.T) {
	p := Principal{Enabled: true, Grants: []Grant{{Role: Admin, HostID: "a"}, {Role: Viewer, HostID: "b"}}}
	for _, tt := range []struct {
		grant Grant
		want  bool
	}{
		{Grant{Role: Admin, HostID: "a"}, true}, {Grant{Role: Viewer, HostID: "b"}, true},
		{Grant{Role: Admin, HostID: "b"}, false}, {Grant{Role: Viewer, All: true}, false},
		{Grant{Role: Owner, All: true}, false}, {Grant{Role: Viewer}, false}, {Grant{Role: Viewer, All: true, HostID: "a"}, false},
	} {
		if got := p.CanDelegate(tt.grant); got != tt.want {
			t.Errorf("%+v: got %v want %v", tt.grant, got, tt.want)
		}
	}
}
