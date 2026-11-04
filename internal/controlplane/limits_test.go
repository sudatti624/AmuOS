package controlplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientKeyGroupsIPv6Prefix(t *testing.T) {
	cases := map[string]string{
		"192.0.2.1:1234":       "192.0.2.1",
		"[2001:db8::1]:1":      "2001:db8::/64",
		"[2001:db8::ffff:1]:2": "2001:db8::/64",
		"[2001:db8:0:1::1]:3":  "2001:db8:0:1::/64",
		"[::ffff:192.0.2.9]:4": "192.0.2.9",
		"not-an-address":       "not-an-address",
	}
	for addr, want := range cases {
		if got := clientKey(addr); got != want {
			t.Errorf("clientKey(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestFullAuthTableDoesNotLockOutNewClients(t *testing.T) {
	db, _, _ := accountFixture(t)
	s, h, err := newServer(db, "https://amuos.test")
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	for i := 0; i < authClients; i++ {
		s.attempts[fmt.Sprintf("198.18.%d.%d", i/256, i%256)] = bucket{Count: authAttempts, Until: until}
	}
	login := func(addr string) int {
		r := httptest.NewRequest("POST", "https://amuos.test/api/v1/auth/login", strings.NewReader(`{"unexpected":1}`))
		r.RemoteAddr = addr
		r.Header.Set("Origin", "https://amuos.test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	// New clients share the overflow bucket instead of being refused outright.
	for i := 0; i < authOverflowLimit; i++ {
		if code := login(fmt.Sprintf("203.0.113.%d:1", i%250)); code != 400 {
			t.Fatalf("attempt %d from a new client got %d", i, code)
		}
	}
	// The overflow bucket is still limited.
	if code := login("192.0.2.200:1"); code != 429 {
		t.Fatalf("overflow bucket was not limited: %d", code)
	}
	// An existing full client keeps its own limit.
	if code := login("198.18.0.1:1"); code != 429 {
		t.Fatalf("known client bypassed its limit: %d", code)
	}
}

func TestRejectionAuditIsBounded(t *testing.T) {
	db, _, owner := accountFixture(t)
	s, h, err := newServer(db, "https://amuos.test")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	now := base
	s.clock = func() time.Time { return now }
	get := func(addr string) {
		r := httptest.NewRequest("GET", "https://amuos.test/missing", nil)
		r.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 || !strings.Contains(w.Body.String(), "not_found") {
			t.Fatalf("suppressed rejection changed the response: %d %s", w.Code, w.Body.String())
		}
	}
	const sent = 200
	for i := 0; i < sent; i++ {
		get("192.0.2.10:1")
	}
	countRejects := func() (rejects int, summary map[string]any) {
		events, err := db.Events(t.Context(), owner.Token, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			switch e.Action {
			case "http.reject":
				rejects++
			case "http.reject.suppressed":
				if err := json.NewDecoder(bytes.NewReader(e.Details)).Decode(&summary); err != nil {
					t.Fatal(err)
				}
			}
		}
		return rejects, summary
	}
	if rejects, summary := countRejects(); rejects != rejectClientLimit || summary != nil {
		t.Fatalf("one client recorded %d rejections, summary %v", rejects, summary)
	}
	// After the window ends, the next rejection writes one summary event.
	now = base.Add(rejectWindow + time.Second)
	get("192.0.2.10:1")
	rejects, summary := countRejects()
	if summary == nil || summary["suppressed_count"] != float64(sent-rejectClientLimit) {
		t.Fatalf("missing or wrong summary: %v", summary)
	}
	if rejects != rejectClientLimit+1 {
		t.Fatalf("new window was not recorded: %d", rejects)
	}
}

func TestRejectionAuditHasGlobalLimit(t *testing.T) {
	db, _, _ := accountFixture(t)
	s, _, err := newServer(db, "https://amuos.test")
	if err != nil {
		t.Fatal(err)
	}
	recorded := 0
	for i := 0; i < rejectGlobalLimit+50; i++ {
		r := httptest.NewRequest("GET", "https://amuos.test/missing", nil)
		r.RemoteAddr = fmt.Sprintf("198.51.%d.%d:1", i/250, i%250)
		if record, _ := s.admitRejection(r); record {
			recorded++
		}
	}
	if recorded != rejectGlobalLimit {
		t.Fatalf("recorded %d, want global limit %d", recorded, rejectGlobalLimit)
	}
}
