package controlplane

import (
	"context"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/store"
)

func TestOwnerTransferHTTP(t *testing.T) {
	db, h, owner := accountFixture(t)
	ctx := context.Background()
	now := time.Now()
	inv, err := db.CreateInvitation(ctx, owner.Token, owner.CSRF, "successor", []rbac.Grant{{Role: rbac.Viewer, All: true}}, now)
	if err != nil {
		t.Fatal(err)
	}
	u, err := db.AcceptInvitation(ctx, inv.Token, "correct horse battery staple", now)
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.Login(ctx, "successor", "correct horse battery staple", now)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/users/" + u.ID + "/owner"
	if w := accountHTTP(t, h, "PUT", path, map[string]bool{"enabled": true}, member, member.CSRF); w.Code != 403 {
		t.Fatalf("viewer escalation: %d", w.Code)
	}
	if w := accountHTTP(t, h, "PUT", path, map[string]bool{"enabled": true}, owner, owner.CSRF); w.Code != 403 {
		t.Fatalf("missing reauth: %d", w.Code)
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/auth/reauthenticate", map[string]string{"password": "correct horse battery staple"}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "PUT", path, map[string]any{}, owner, owner.CSRF); w.Code != 400 {
		t.Fatalf("missing bool: %d", w.Code)
	}
	if w := accountHTTP(t, h, "PUT", path, map[string]bool{"enabled": true}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/me", nil, member, ""); w.Code != 401 {
		t.Fatalf("old promoted session: %d", w.Code)
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/ownership/transfer", map[string]string{"user_id": u.ID}, owner, ""); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := accountHTTP(t, h, "POST", "/api/v1/ownership/transfer", map[string]string{"user_id": u.ID}, owner, owner.CSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatal("transfer did not clear browser cookie")
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/me", nil, owner, ""); w.Code != 401 {
		t.Fatalf("transfer source session survived: %d", w.Code)
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/ownership/transfer", map[string]string{"user_id": owner.UserID}, store.Session{}, ""); w.Code != 401 {
		t.Fatalf("anonymous transfer: %d", w.Code)
	}
}
