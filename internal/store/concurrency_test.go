package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestConcurrentSetupConsumesTokenOnce(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	token, err := s.PrepareBootstrap(ctx, testTime)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"first-owner", "second-owner"} {
		wg.Add(1)
		go func(name string) { defer wg.Done(); results <- s.Setup(ctx, token, name, testPassword, testTime) }(name)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrCredentials) && !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("registered %d Owners using one token", successes)
	}
}

func TestWrongPasswordProducesAuditWithoutSession(t *testing.T) {
	s, session, _ := prepared(t)
	ctx := context.Background()
	if _, err := s.Login(ctx, "owner", "wrong password", testTime); !errors.Is(err, ErrCredentials) {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "unknown-user", testPassword, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("sessions=%d err=%v", count, err)
	}
	events, err := s.Events(ctx, session.Token, testTime)
	if err != nil {
		t.Fatal(err)
	}
	denied := 0
	for _, e := range events {
		if e.Action == "auth.login" && e.Outcome == "denied" {
			denied++
		}
	}
	if denied != 2 {
		t.Fatalf("expected two denial events; got %d", denied)
	}
}
