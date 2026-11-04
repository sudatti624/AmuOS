package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/webui"
)

func TestIdentityIncludesOwnUsername(t *testing.T) {
	_, h, owner := accountFixture(t)
	r := httptest.NewRequest("GET", "https://amuos.test/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: owner.Token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var body struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Username != "owner" || body.ID != owner.UserID {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "https://amuos.test/api/v1/me", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || strings.Contains(w.Body.String(), "owner") {
		t.Fatal("anonymous identity", w.Code, w.Body.String())
	}
}

func TestWebShellAndAssets(t *testing.T) {
	db, h, owner := accountFixture(t)
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://amuos.test"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	nonces := map[string]bool{}
	for _, page := range webui.Pages {
		w := get(page)
		if !webui.Built() {
			if w.Code != 503 {
				t.Fatalf("%s without build got %d", page, w.Code)
			}
			continue
		}
		csp := w.Header().Get("Content-Security-Policy")
		_, after, found := strings.Cut(csp, "'nonce-")
		nonce, _, _ := strings.Cut(after, "'")
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !found || csp != webui.CSP(nonce) {
			t.Fatalf("%s got %d %v", page, w.Code, w.Header())
		}
		// The nonce is fresh per response and replaces the build placeholder.
		if len(nonce) < 32 || nonces[nonce] || !strings.Contains(w.Body.String(), `nonce="`+nonce+`"`) || strings.Contains(w.Body.String(), "AMUOS_CSP_NONCE") {
			t.Fatalf("%s nonce %q not applied", page, nonce)
		}
		nonces[nonce] = true
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s shell is cacheable", page)
		}
	}
	for _, path := range []string{"/assets/missing.js", "/assets/.hidden.js", "/assets/../api/v1/users", "/assets/index.html", "/favicon.ico", "/unknown"} {
		if w := get(path); w.Code != 404 || !strings.Contains(w.Body.String(), "not_found") {
			t.Fatalf("%s got %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := get("/api/v1/hosts"); w.Header().Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
		t.Fatal("API CSP changed", w.Header())
	}
	events, err := db.Events(t.Context(), owner.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rejected := 0
	for _, event := range events {
		if event.Action == "http.reject" && event.Reason == "not_found" {
			rejected++
		}
	}
	if rejected < 6 {
		t.Fatalf("missing files were not audited: %d", rejected)
	}
}
