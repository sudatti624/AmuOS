package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/store"
)

func TestHTTPAuditCorrelationAndSecretExclusion(t *testing.T) {
	db, h, owner := accountFixture(t)
	w := accountHTTP(t, h, "POST", "/api/v1/hosts", map[string]string{"name": "server"}, owner, owner.CSRF)
	if w.Code != 201 || len(w.Header().Get("X-Request-ID")) != 43 {
		t.Fatal(w.Code, w.Body.String())
	}
	events, err := db.Events(context.Background(), owner.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if events[0].RequestID != w.Header().Get("X-Request-ID") {
		t.Fatal("mutation request ID does not match header")
	}
	const secret = "do-not-record-this-secret"
	r := httptest.NewRequest("PUT", "https://amuos.test/api/v1/users/"+secret+"/owner?token="+secret, strings.NewReader(`{"password":"`+secret+`"}`))
	r.Header.Set("Origin", "https://evil.test/"+secret)
	r.Header.Set("X-Request-ID", secret)
	r.Header.Set("X-CSRF-Token", secret)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: owner.Token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || w.Header().Get("X-Request-ID") == secret {
		t.Fatal(w.Code, w.Header())
	}
	events, err = db.Events(context.Background(), owner.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e := events[0]
	if e.Action != "http.reject" || e.RequestID != w.Header().Get("X-Request-ID") || e.Actor != owner.UserID || e.Target != "PUT /api/v1/users/{id}/owner" || e.Reason != "origin_rejected" {
		t.Fatalf("bad rejection: %+v", e)
	}
	raw, _ := json.Marshal(events)
	for _, value := range []string{secret, owner.Token, owner.CSRF} {
		if strings.Contains(string(raw), value) {
			t.Fatal("secret in audit payload")
		}
	}
}

func TestEntryRejectionsAreAudited(t *testing.T) {
	db, h, owner := accountFixture(t)
	for i := 0; i < 6; i++ {
		w := accountHTTP(t, h, "POST", "/api/v1/auth/login", map[string]string{"unexpected": "secret"}, store.Session{}, "")
		want := 400
		reason := "invalid_json"
		if i == 5 {
			want = 429
			reason = "rate_limited"
		}
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		events, err := db.Events(context.Background(), owner.Token, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if events[0].Reason != reason || events[0].RequestID != w.Header().Get("X-Request-ID") {
			t.Fatalf("missing rejection: %+v", events[0])
		}
	}
	r := httptest.NewRequest("GET", "https://amuos.test/missing/secret?token=secret", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	events, err := db.Events(context.Background(), owner.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Target != "GET unmatched" {
		t.Fatalf("raw unmatched URL stored: %+v", events[0])
	}
}

func TestRejectionAuditSurvivesCancellation(t *testing.T) {
	db, h, owner := accountFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "https://amuos.test/api/v1/hosts", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	events, err := db.Events(context.Background(), owner.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if events[0].RequestID != w.Header().Get("X-Request-ID") || events[0].Reason != "origin_rejected" {
		t.Fatal("cancelled peer suppressed rejection audit")
	}
}

func TestAuditFailureIsVisibleAndHealthStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.Exec("CREATE TRIGGER fail_entry_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'must-not-expose-this-error'); END"); err != nil {
		t.Fatal(err)
	}
	h, err := New(db, "https://amuos.test")
	if err != nil {
		t.Fatal(err)
	}
	w := accountHTTP(t, h, "POST", "/api/v1/groups", map[string]any{}, store.Session{}, "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "must-not-expose") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = accountHTTP(t, h, "GET", "/healthz", nil, store.Session{}, "")
	if w.Code != 200 {
		t.Fatalf("read-only health blocked by audit error: %d", w.Code)
	}
}

func TestRequestIDsAreUniqueUnderConcurrency(t *testing.T) {
	_, h, _ := accountFixture(t)
	ids := make(chan string, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "https://amuos.test/healthz", nil))
			ids <- w.Header().Get("X-Request-ID")
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if len(id) != 43 || seen[id] {
			t.Fatalf("invalid/duplicate ID %q", id)
		}
		seen[id] = true
	}
}
