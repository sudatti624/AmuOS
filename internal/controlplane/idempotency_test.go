package controlplane

import (
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

func TestHostCreationRequiresAndReplaysIdempotencyKey(t *testing.T) {
	db, h, owner := accountFixture(t)
	key := security.Token()
	request := func(key, name string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://amuos.test/api/v1/hosts", strings.NewReader(`{"name":"`+name+`"}`))
		r.Header.Set("Origin", "https://amuos.test")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", owner.CSRF)
		r.Header.Set("Idempotency-Key", key)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: owner.Token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("", "server"); w.Code != 400 {
		t.Fatalf("missing key: %d %s", w.Code, w.Body.String())
	}
	first, second := request(key, "server"), request(key, "server")
	if first.Code != 201 || second.Code != 201 || second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal(first.Code, second.Code, second.Body.String())
	}
	var a, b store.Host
	if err := json.Unmarshal(first.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatal("HTTP retry duplicated host")
	}
	if first.Header().Get("X-Request-ID") == second.Header().Get("X-Request-ID") {
		t.Fatal("request ID reused as operation identity")
	}
	if w := request(key, "other"); w.Code != 409 {
		t.Fatalf("different input accepted: %d", w.Code)
	}
	events, err := db.Events(context.Background(), owner.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	replay := false
	for _, e := range events {
		if e.Action == "host.create.replay" && e.RequestID == second.Header().Get("X-Request-ID") {
			replay = true
		}
	}
	if !replay {
		t.Fatal("replay audit missing")
	}
}
