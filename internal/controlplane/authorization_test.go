package controlplane

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/store"
)

func TestGroupAndGrantManagementHTTP(t *testing.T) {
	db, h, owner := accountFixture(t)
	ctx := context.Background()
	now := time.Now()
	inv, err := db.CreateInvitation(ctx, owner.Token, owner.CSRF, "member", []rbac.Grant{{Role: rbac.Viewer, All: true}}, now)
	if err != nil {
		t.Fatal(err)
	}
	u, err := db.AcceptInvitation(ctx, inv.Token, "correct horse battery staple", now)
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.Login(ctx, "member", "correct horse battery staple", now)
	if err != nil {
		t.Fatal(err)
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/groups", nil, member, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/groups", map[string]string{"name": "team"}, owner, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := accountHTTP(t, h, "POST", "/api/v1/groups", map[string]string{"name": "team"}, owner, owner.CSRF)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var group store.Group
	if err = json.Unmarshal(w.Body.Bytes(), &group); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/groups/" + group.ID
	if w := accountHTTP(t, h, "PUT", base+"/members", map[string]any{}, owner, owner.CSRF); w.Code != 400 {
		t.Fatalf("missing members: %d", w.Code)
	}
	if w := accountHTTP(t, h, "PUT", base+"/members", map[string]any{"user_ids": []string{u.ID}}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "PUT", base+"/grants", map[string]any{"grants": []rbac.Grant{{Role: rbac.Admin, All: true}}}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/users", nil, member, ""); w.Code != 200 {
		t.Fatalf("new group permission not applied: %d %s", w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/users/"+u.ID+"/grants", nil, owner, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "PUT", "/api/v1/users/"+u.ID+"/grants", map[string]any{"grants": []rbac.Grant{}}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "DELETE", base, nil, owner, owner.CSRF); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/users", nil, member, ""); w.Code != 403 {
		t.Fatalf("deleted group retained permission: %d", w.Code)
	}
}
