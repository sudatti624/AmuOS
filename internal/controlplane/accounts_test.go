package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
	"github.com/sudatti624/AmuOS/internal/store"
)

func accountHTTP(t *testing.T, h http.Handler, method, path string, body any, session store.Session, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, "https://amuos.test"+path, bytes.NewReader(data))
	r.Header.Set("Origin", "https://amuos.test")
	r.Header.Set("Content-Type", "application/json")
	if method == "POST" && path == "/api/v1/hosts" {
		r.Header.Set("Idempotency-Key", security.Token())
	}
	r.Header.Set("X-CSRF-Token", csrf)
	if session.Token != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: session.Token})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func accountFixture(t *testing.T) (*store.Store, http.Handler, store.Session) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	token, err := db.PrepareBootstrap(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Setup(ctx, token, "owner", "correct horse battery staple", now); err != nil {
		t.Fatal(err)
	}
	session, err := db.Login(ctx, "owner", "correct horse battery staple", now)
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(db, "https://amuos.test")
	if err != nil {
		t.Fatal(err)
	}
	return db, h, session
}

func TestAccountLifecycleHTTP(t *testing.T) {
	_, h, owner := accountFixture(t)
	create := map[string]any{"username": "member", "grants": []rbac.Grant{{Role: rbac.Viewer, All: true}}}
	if w := accountHTTP(t, h, "POST", "/api/v1/invitations", create, owner, ""); w.Code != 403 {
		t.Fatalf("missing CSRF: %d %s", w.Code, w.Body.String())
	}
	w := accountHTTP(t, h, "POST", "/api/v1/invitations", create, owner, owner.CSRF)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var inv store.Invitation
	if err := json.Unmarshal(w.Body.Bytes(), &inv); err != nil {
		t.Fatal(err)
	}
	accept := map[string]any{"token": inv.Token, "password": "correct horse battery staple", "grants": []rbac.Grant{{Role: rbac.Owner, All: true}}}
	if w := accountHTTP(t, h, "POST", "/api/v1/invitations/accept", accept, store.Session{}, ""); w.Code != 400 {
		t.Fatalf("altered scope accepted: %d %s", w.Code, w.Body.String())
	}
	delete(accept, "grants")
	w = accountHTTP(t, h, "POST", "/api/v1/invitations/accept", accept, store.Session{}, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("signup unexpectedly authenticated client")
	}
	var member store.User
	if err := json.Unmarshal(w.Body.Bytes(), &member); err != nil {
		t.Fatal(err)
	}
	w = accountHTTP(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "member", "password": "correct horse battery staple"}, store.Session{}, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var auth struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	memberSession := store.Session{Token: w.Result().Cookies()[0].Value, CSRF: auth.CSRF}
	if w := accountHTTP(t, h, "GET", "/api/v1/users", nil, memberSession, ""); w.Code != 403 {
		t.Fatalf("viewer lists users: %d", w.Code)
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/users", nil, owner, ""); w.Code != 200 || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("user listing %d %s", w.Code, w.Body.String())
	}
	path := "/api/v1/users/" + member.ID + "/enabled"
	if w := accountHTTP(t, h, "PATCH", path, map[string]any{}, owner, owner.CSRF); w.Code != 400 {
		t.Fatalf("missing enabled field: %d", w.Code)
	}
	if w := accountHTTP(t, h, "PATCH", path, map[string]bool{"enabled": false}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/me", nil, memberSession, ""); w.Code != 401 {
		t.Fatalf("disabled session: %d", w.Code)
	}
	if w := accountHTTP(t, h, "PATCH", path, map[string]bool{"enabled": true}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/me", nil, memberSession, ""); w.Code != 401 {
		t.Fatalf("old session revived: %d", w.Code)
	}
	if w := accountHTTP(t, h, "DELETE", "/api/v1/invitations/"+inv.ID, nil, owner, owner.CSRF); w.Code != 409 {
		t.Fatalf("revoke consumed invite: %d", w.Code)
	}
}

func TestPasswordAndOwnerGuardHTTP(t *testing.T) {
	_, h, owner := accountFixture(t)
	path := "/api/v1/users/" + owner.UserID + "/enabled"
	if w := accountHTTP(t, h, "PATCH", path, map[string]bool{"enabled": false}, owner, owner.CSRF); w.Code != 403 {
		t.Fatalf("missing reauth: %d", w.Code)
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/auth/reauthenticate", map[string]string{"password": "correct horse battery staple"}, owner, owner.CSRF); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "PATCH", path, map[string]bool{"enabled": false}, owner, owner.CSRF); w.Code != 409 {
		t.Fatalf("last Owner: %d %s", w.Code, w.Body.String())
	}
	body := map[string]string{"current_password": "correct horse battery staple", "new_password": "replacement secure password"}
	if w := accountHTTP(t, h, "POST", "/api/v1/auth/password", body, owner, ""); w.Code != 403 {
		t.Fatalf("password CSRF: %d", w.Code)
	}
	w := accountHTTP(t, h, "POST", "/api/v1/auth/password", body, owner, owner.CSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatal("password change did not clear browser cookie")
	}
	if w := accountHTTP(t, h, "GET", "/api/v1/me", nil, owner, ""); w.Code != 401 {
		t.Fatalf("old session: %d", w.Code)
	}
	w = accountHTTP(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": "owner", "password": "replacement secure password"}, store.Session{}, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestInvitationRevocationHTTP(t *testing.T) {
	_, h, owner := accountFixture(t)
	w := accountHTTP(t, h, "POST", "/api/v1/invitations", map[string]any{"username": "pending", "grants": []rbac.Grant{{Role: rbac.Viewer, All: true}}}, owner, owner.CSRF)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var inv store.Invitation
	if err := json.Unmarshal(w.Body.Bytes(), &inv); err != nil {
		t.Fatal(err)
	}
	if w := accountHTTP(t, h, "DELETE", "/api/v1/invitations/"+inv.ID, nil, owner, owner.CSRF); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "POST", "/api/v1/invitations/accept", map[string]string{"token": inv.Token, "password": "correct horse battery staple"}, store.Session{}, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := accountHTTP(t, h, "DELETE", "/api/v1/invitations/missing", nil, owner, owner.CSRF); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}
