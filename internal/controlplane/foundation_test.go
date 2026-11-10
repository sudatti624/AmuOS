package controlplane

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/store"
)

// Exercise scope composition through HTTP with real sessions, not a mocked principal.
func TestFoundationHostAuthorizationMatrix(t *testing.T) {
	db, h, owner := accountFixture(t)
	ctx, now := t.Context(), time.Now()
	a, err := db.CreateHost(ctx, owner.Token, owner.CSRF, "scope-a", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateHost(ctx, owner.Token, owner.CSRF, "scope-b", now)
	if err != nil {
		t.Fatal(err)
	}
	for i, role := range []rbac.Role{rbac.Admin, rbac.Operator, rbac.Viewer} {
		t.Run(string(role), func(t *testing.T) {
			name := fmt.Sprintf("matrix-%d", i)
			inv, err := db.CreateInvitation(ctx, owner.Token, owner.CSRF, name, []rbac.Grant{{Role: role, HostID: a.ID}, {Role: rbac.Viewer, HostID: b.ID}}, now)
			if err != nil {
				t.Fatal(err)
			}
			user, err := db.AcceptInvitation(ctx, inv.Token, "correct horse battery staple", now)
			if err != nil {
				t.Fatal(err)
			}
			session, err := db.Login(ctx, name, "correct horse battery staple", now)
			if err != nil {
				t.Fatal(err)
			}
			for _, host := range []store.Host{a, b} {
				base := "/api/v1/hosts/" + host.ID
				for _, suffix := range []string{"", "/metrics?range=1h", "/systemd", "/tasks"} {
					w := accountHTTP(t, h, "GET", base+suffix, nil, session, "")
					if w.Code != 200 {
						t.Fatalf("read %s: %d %s", suffix, w.Code, w.Body.String())
					}
				}
				want := 403
				if host.ID == a.ID && role == rbac.Admin {
					want = 201
				}
				w := accountHTTP(t, h, "POST", base+"/enrollment", map[string]int{"ttl_minutes": 10}, session, session.CSRF)
				if w.Code != want {
					t.Fatalf("enrollment %s: %d want %d: %s", host.Name, w.Code, want, w.Body.String())
				}
			}
			// A host Admin must not borrow the other grant's scope or become global Admin.
			for _, path := range []string{"/api/v1/users", "/api/v1/groups", "/api/v1/monitoring/settings"} {
				if w := accountHTTP(t, h, "GET", path, nil, session, ""); w.Code != 403 {
					t.Fatalf("global %s: %d", path, w.Code)
				}
			}
			if err := db.SetUserGrants(ctx, owner.Token, owner.CSRF, user.ID, []rbac.Grant{{Role: rbac.Viewer, HostID: b.ID}}, now); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"", "/metrics?range=1h", "/systemd", "/tasks"} {
				if w := accountHTTP(t, h, "GET", "/api/v1/hosts/"+a.ID+suffix, nil, session, ""); w.Code != 404 {
					t.Fatalf("removed scope %s: %d", suffix, w.Code)
				}
			}
			w := accountHTTP(t, h, "GET", "/api/v1/hosts", nil, session, "")
			var list struct {
				Items []store.Host `json:"items"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || len(list.Items) != 1 || list.Items[0].ID != b.ID {
				t.Fatalf("filtered list: %s", w.Body.String())
			}
			if err := db.SetUserEnabled(ctx, owner.Token, owner.CSRF, user.ID, false, now); err != nil {
				t.Fatal(err)
			}
			if w := accountHTTP(t, h, "GET", "/api/v1/hosts/"+b.ID, nil, session, ""); w.Code != 401 {
				t.Fatalf("disabled: %d", w.Code)
			}
			if err := db.SetUserEnabled(ctx, owner.Token, owner.CSRF, user.ID, true, now); err != nil {
				t.Fatal(err)
			}
			if w := accountHTTP(t, h, "GET", "/api/v1/me", nil, session, ""); w.Code != 401 {
				t.Fatalf("session resurrected: %d", w.Code)
			}
		})
	}
	// Unknown future operations fail closed, including attempts to smuggle a command.
	for _, path := range []string{"deployments", "containers", "vms", "systemd/restart"} {
		w := accountHTTP(t, h, "POST", "/api/v1/hosts/"+a.ID+"/"+path, map[string]string{"command": "id"}, owner, owner.CSRF)
		if w.Code != 404 || strings.Contains(w.Body.String(), "scope-a") {
			t.Fatalf("future operation %s: %d", path, w.Code)
		}
	}
}
