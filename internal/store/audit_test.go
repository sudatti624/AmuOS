package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/security"
)

func TestAuditRequestIDPersistsWithMutation(t *testing.T) {
	s, owner, path := prepared(t)
	id := security.Token()
	ctx := WithRequestID(context.Background(), id)
	if _, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "server", testTime); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	events, err := reopened.Events(context.Background(), owner.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Action != "host.create" || events[0].RequestID != id {
		t.Fatalf("missing request correlation: %+v", events[0])
	}
	if events[len(events)-1].RequestID != "" {
		t.Fatal("local event fabricated an HTTP request ID")
	}
	if RequestID(WithRequestID(context.Background(), "untrusted\nsecret")) != "" {
		t.Fatal("invalid metadata accepted")
	}
}

func TestHTTPRejectionDoesNotRefreshSession(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := WithRequestID(context.Background(), security.Token())
	if err := s.RecordHTTPRejection(ctx, owner.Token, "POST /api/v1/groups", "origin_rejected", testTime.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var last int64
	if err := s.db.QueryRow("SELECT last_seen FROM sessions WHERE user_id=?", owner.UserID).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if last != testTime.Unix() {
		t.Fatal("rejected cross-origin request kept session alive")
	}
	if err := s.RecordHTTPRejection(ctx, owner.Token, "POST /api/v1/groups", "origin_rejected", testTime.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var actor string
	if err := s.db.QueryRow("SELECT actor_id FROM audit_events ORDER BY id DESC LIMIT 1").Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if actor != "anonymous" {
		t.Fatal("expired session attributed to a user")
	}
	events, err := s.Events(context.Background(), owner.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), owner.Token) || strings.Contains(string(raw), owner.CSRF) {
		t.Fatal("credential leaked to audit")
	}
}

func TestCorruptAuditDetailsAreRejected(t *testing.T) {
	s, owner, _ := prepared(t)
	if _, err := s.db.Exec("UPDATE audit_events SET details='invalid-json' WHERE action='setup.complete'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Events(context.Background(), owner.Token, testTime); err == nil {
		t.Fatal("corrupt audit JSON returned as successful response")
	}
}
