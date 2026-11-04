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

	"github.com/sudatti624/AmuOS/internal/security"
	"github.com/sudatti624/AmuOS/internal/store"
)

func TestAuthenticatedHTTPFlow(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setupToken, err := db.PrepareBootstrap(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	const origin = "https://amuos.test"
	h, err := New(db, origin)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body any, cookie *http.Cookie, csrf, from string) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, origin+path, bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		if method == "POST" && path == "/api/v1/hosts" {
			r.Header.Set("Idempotency-Key", security.Token())
		}
		r.Header.Set("Origin", from)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/v1/hosts", nil, nil, "", origin); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	setup := map[string]string{"token": setupToken, "username": "owner", "password": "correct horse battery staple"}
	if w := request("POST", "/api/v1/setup", setup, nil, "", "https://evil.test"); w.Code != 403 {
		t.Fatal("cross-origin setup", w.Code)
	}
	if w := request("POST", "/api/v1/setup", setup, nil, "", origin); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	login := map[string]string{"username": "owner", "password": "correct horse battery staple"}
	w := request("POST", "/api/v1/auth/login", login, nil, "", origin)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.Domain != "" {
		t.Fatalf("unsafe cookie %+v", cookie)
	}
	var auth struct {
		CSRF string `json:"csrf_token"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/v1/hosts", map[string]string{"name": "a"}, cookie, "", origin); w.Code != 403 {
		t.Fatal("missing csrf", w.Code)
	}
	if w := request("POST", "/api/v1/hosts", map[string]string{"name": "a"}, cookie, auth.CSRF, origin); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", "/api/v1/hosts", nil, cookie, "", origin); w.Code != 200 || !strings.Contains(w.Body.String(), "pending") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", "/api/v1/audit", nil, cookie, "", origin); w.Code != 200 || strings.Contains(w.Body.String(), setupToken) || strings.Contains(w.Body.String(), auth.CSRF) {
		t.Fatal("audit", w.Code, w.Body.String())
	}
	if w := request("POST", "/api/v1/auth/logout", nil, cookie, auth.CSRF, origin); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", "/api/v1/me", nil, cookie, "", origin); w.Code != 401 {
		t.Fatal("reused session", w.Code)
	}
}

func TestLoginRateLimitAndStrictJSON(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err := New(db, "https://amuos.test")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest("POST", "https://amuos.test/api/v1/auth/login", strings.NewReader(`{"username":"a","password":"b","unexpected":true}`))
		r.Header.Set("Origin", "https://amuos.test")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", string(rune('a'+i)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 400
		if i == 5 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d: got %d want %d", i, w.Code, want)
		}
	}
	if _, err := New(db, "http://localhost:8443"); err == nil {
		t.Fatal("insecure origin accepted")
	}
}

func TestDisabledFeaturesAreAbsent(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err := New(db, "https://amuos.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/docker", "/api/v1/deploy", "/api/v1/files", "/api/v1/vms", "/api/v1/services/start"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 404 {
			t.Fatalf("unexpected endpoint %s: %d", path, w.Code)
		}
	}
}
