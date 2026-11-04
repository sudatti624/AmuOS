package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

func TestHostIdempotencyPersistsAcrossRestart(t *testing.T) {
	s, owner, path := prepared(t)
	key := security.Token()
	ctx := WithIdempotencyKey(context.Background(), key)
	first, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "server", testTime)
	if err != nil || first.Replayed {
		t.Fatal(first, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	retry, err := reopened.CreateHost(ctx, owner.Token, owner.CSRF, " server ", testTime)
	if err != nil || !retry.Replayed || retry.ID != first.ID {
		t.Fatalf("duplicate after lost response/restart: %+v %v", retry, err)
	}
	if _, err = reopened.CreateHost(ctx, owner.Token, owner.CSRF, "different", testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("key accepted different input: %v", err)
	}
	var hosts int
	if err = reopened.db.QueryRow("SELECT count(*) FROM hosts").Scan(&hosts); err != nil || hosts != 1 {
		t.Fatal(hosts, err)
	}
	var stored []byte
	if err = reopened.db.QueryRow("SELECT key_hash FROM idempotency_records").Scan(&stored); err != nil || len(stored) != 32 || string(stored) == key {
		t.Fatal("plaintext key persisted", err)
	}
}

func TestConcurrentHostCreationReplaysOneResult(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := WithIdempotencyKey(context.Background(), security.Token())
	var wg sync.WaitGroup
	type result struct {
		Host Host
		Err  error
	}
	results := make(chan result, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			host, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "server", testTime)
			results <- result{host, err}
		}()
	}
	wg.Wait()
	close(results)
	id := ""
	original := 0
	for r := range results {
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		if id == "" {
			id = r.Host.ID
		}
		if id != r.Host.ID {
			t.Fatal("concurrent duplicate created")
		}
		if !r.Host.Replayed {
			original++
		}
	}
	if original != 1 {
		t.Fatalf("original executions=%d", original)
	}
}

func TestIdempotencyRechecksActorPermissions(t *testing.T) {
	s, owner, _ := prepared(t)
	base := context.Background()
	user, admin := inviteUser(t, s, owner, "admin", rbac.Grant{Role: rbac.Admin, All: true})
	ctx := WithIdempotencyKey(base, security.Token())
	first, err := s.CreateHost(ctx, admin.Token, admin.CSRF, "server", testTime)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "server", testTime)
	if err != nil || other.ID == first.ID {
		t.Fatal("actor scope mixed", err)
	}
	if err = s.SetUserGrants(base, owner.Token, owner.CSRF, user.ID, []rbac.Grant{{Role: rbac.Viewer, All: true}}, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateHost(ctx, admin.Token, admin.CSRF, "server", testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("replay bypassed current authorization: %v", err)
	}
}

func TestIdempotencyAtomicityAndExpiryTombstone(t *testing.T) {
	s, owner, _ := prepared(t)
	ctx := WithIdempotencyKey(context.Background(), security.Token())
	if _, err := s.db.Exec("CREATE TRIGGER fail_idempotent_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit failed'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "server", testTime); err == nil {
		t.Fatal("created without audit")
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM idempotency_records").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial operation record", count, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_idempotent_audit"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "server", testTime); err != nil {
		t.Fatal(err)
	}
	later := testTime.Add(30 * 24 * time.Hour)
	if err := s.ExpireIdempotency(context.Background(), later); err != nil {
		t.Fatal(err)
	}
	var state string
	var hash []byte
	if err := s.db.QueryRow("SELECT state,input_hash FROM idempotency_records").Scan(&state, &hash); err != nil || state != "expired" || hash != nil {
		t.Fatal(state, err)
	}
	fresh, err := s.Login(context.Background(), "owner", testPassword, later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateHost(ctx, fresh.Token, fresh.CSRF, "server", later); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired key created duplicate: %v", err)
	}
	if err = s.db.QueryRow("SELECT count(*) FROM hosts").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
