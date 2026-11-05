package store

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/rbac"
)

type testAgent struct {
	key        *ecdsa.PrivateKey
	credential AgentCredential
	serial     string
}

func agentKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "agent"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, der
}

func serialOf(t *testing.T, certPEM string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return agentca.SerialString(cert.SerialNumber)
}

func agentFixture(t *testing.T) (*Store, Session, Host, *agentca.CA) {
	t.Helper()
	s, owner, host := taskFixture(t)
	ca, err := agentca.Load(t.TempDir(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	return s, owner, host, ca
}

func enrollAgent(t *testing.T, s *Store, owner Session, host Host, ca *agentca.CA) testAgent {
	t.Helper()
	enrollment, err := s.CreateEnrollment(context.Background(), owner.Token, owner.CSRF, host.ID, 10*time.Minute, testTime)
	if err != nil {
		t.Fatal(err)
	}
	key, csr := agentKey(t)
	credential, err := s.Enroll(context.Background(), enrollment.Code, csr, ca, testTime)
	if err != nil {
		t.Fatal(err)
	}
	return testAgent{key: key, credential: credential, serial: serialOf(t, credential.Certificate)}
}

func hostStateOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	var state string
	if err := s.db.QueryRow("SELECT state FROM hosts WHERE id=?", id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestEnrollmentCodesAreScopedAndSingleUse(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	other, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "other", testTime)
	if err != nil {
		t.Fatal(err)
	}
	_, operator := inviteUser(t, s, owner, "operator", rbac.Grant{Role: rbac.Operator, HostID: host.ID})
	_, admin := inviteUser(t, s, owner, "hostadmin", rbac.Grant{Role: rbac.Admin, HostID: host.ID})
	if _, err := s.CreateEnrollment(ctx, operator.Token, operator.CSRF, host.ID, 10*time.Minute, testTime); !errors.Is(err, ErrDenied) {
		t.Fatalf("operator issued a code: %v", err)
	}
	if _, err := s.CreateEnrollment(ctx, admin.Token, admin.CSRF, other.ID, 10*time.Minute, testTime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("code for a host outside scope: %v", err)
	}
	for _, ttl := range []time.Duration{0, 31 * time.Minute} {
		if _, err := s.CreateEnrollment(ctx, admin.Token, admin.CSRF, host.ID, ttl, testTime); !errors.Is(err, ErrInput) {
			t.Fatalf("ttl %v: %v", ttl, err)
		}
	}
	first, err := s.CreateEnrollment(ctx, admin.Token, admin.CSRF, host.ID, 10*time.Minute, testTime)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateEnrollment(ctx, admin.Token, admin.CSRF, host.ID, time.Minute, testTime)
	if err != nil {
		t.Fatal(err)
	}
	var stored int
	if err := s.db.QueryRow("SELECT count(*) FROM audit_events WHERE details LIKE ?", "%"+second.Code+"%").Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("enrollment code reached the audit log: %d %v", stored, err)
	}
	_, csr := agentKey(t)
	// Reissuing invalidated the first code.
	if _, err := s.Enroll(ctx, first.Code, csr, ca, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("replaced code: %v", err)
	}
	if _, err := s.Enroll(ctx, second.ID+".wrong", csr, ca, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := s.Enroll(ctx, second.Code, csr, ca, testTime.Add(time.Minute)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("expired code: %v", err)
	}
	if _, err := s.Enroll(ctx, second.Code, []byte("not a request"), ca, testTime); !errors.Is(err, ErrInput) {
		t.Fatalf("invalid request: %v", err)
	}
	third, err := s.CreateEnrollment(ctx, admin.Token, admin.CSRF, host.ID, 10*time.Minute, testTime)
	if err != nil {
		t.Fatal(err)
	}
	// Concurrent use of one code binds exactly one Agent.
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, csr := agentKey(t)
			_, err := s.Enroll(ctx, third.Code, csr, ca, testTime)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("concurrent enrollment: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d concurrent enrollments succeeded", succeeded)
	}
	if hostStateOf(t, s, host.ID) != "active" {
		t.Fatal("host did not become active")
	}
	if _, err := s.CreateEnrollment(ctx, admin.Token, admin.CSRF, host.ID, 10*time.Minute, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("code for an enrolled host: %v", err)
	}
}

func TestEnrollmentRecoveryNeedsTheEnrolledKey(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	enrollment, err := s.CreateEnrollment(ctx, owner.Token, owner.CSRF, host.ID, 10*time.Minute, testTime)
	if err != nil {
		t.Fatal(err)
	}
	key, csr := agentKey(t)
	original, err := s.Enroll(ctx, enrollment.Code, csr, ca, testTime)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(key *ecdsa.PrivateKey, challenge, nonce string) []byte {
		signature, err := ecdsa.SignASN1(rand.Reader, key, agentca.RecoverDigest(enrollment.ID, challenge, nonce))
		if err != nil {
			t.Fatal(err)
		}
		return signature
	}
	challenge, nonce, err := s.EnrollmentChallenge(ctx, enrollment.ID, testTime)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _ := agentKey(t)
	if _, err := s.RecoverEnrollment(ctx, enrollment.ID, challenge, nonce, sign(otherKey, challenge, nonce), testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("another key recovered the result: %v", err)
	}
	// The failed attempt spent the challenge.
	if _, err := s.RecoverEnrollment(ctx, enrollment.ID, challenge, nonce, sign(key, challenge, nonce), testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("challenge reused: %v", err)
	}
	challenge, nonce, err = s.EnrollmentChallenge(ctx, enrollment.ID, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecoverEnrollment(ctx, enrollment.ID, challenge, nonce, sign(key, challenge, nonce), testTime.Add(31*time.Second)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("expired challenge: %v", err)
	}
	challenge, nonce, err = s.EnrollmentChallenge(ctx, enrollment.ID, testTime)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.RecoverEnrollment(ctx, enrollment.ID, challenge, nonce, sign(key, challenge, nonce), testTime)
	if err != nil || recovered != original {
		t.Fatalf("recovery: %+v %v", recovered, err)
	}
	if _, _, err := s.EnrollmentChallenge(ctx, "missing", testTime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("challenge for a missing enrollment: %v", err)
	}
	if err := s.RevokeHost(ctx, owner.Token, owner.CSRF, host.ID, testTime); err != nil {
		t.Fatal(err)
	}
	challenge, nonce, err = s.EnrollmentChallenge(ctx, enrollment.ID, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecoverEnrollment(ctx, enrollment.ID, challenge, nonce, sign(key, challenge, nonce), testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("revoked Agent recovered its credential: %v", err)
	}
}

func TestAgentTrustIsCheckedOnEveryRequest(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	agent := enrollAgent(t, s, owner, host, ca)
	if _, err := s.Heartbeat(ctx, agent.serial, agent.credential.AgentID, "0.3.0", []string{"heartbeat"}, testTime); err != nil {
		t.Fatal(err)
	}
	// A serial presented with another Agent's identity is refused.
	if _, err := s.Heartbeat(ctx, agent.serial, "other-agent", "0.3.0", nil, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("mismatched identity: %v", err)
	}
	if _, err := s.Heartbeat(ctx, "ffff", agent.credential.AgentID, "0.3.0", nil, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("unknown serial: %v", err)
	}
	if _, err := s.Heartbeat(ctx, agent.serial, agent.credential.AgentID, "0.3.0", nil, testTime.Add(agentca.CredentialLifetime)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("expired credential: %v", err)
	}
	hosts, err := s.Hosts(ctx, owner.Token, testTime.Add(30*time.Second))
	if err != nil || len(hosts) != 1 || hosts[0].Agent == nil || !hosts[0].Agent.Connected || hosts[0].Agent.Version != "0.3.0" {
		t.Fatalf("host agent info: %+v %v", hosts, err)
	}
	later, err := s.Login(ctx, "owner", testPassword, testTime.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if hosts, err := s.Hosts(ctx, later.Token, testTime.Add(2*time.Minute)); err != nil || hosts[0].Agent.Connected {
		t.Fatalf("stale Agent shown as connected: %v", err)
	}
	owner, now := later, testTime.Add(2*time.Minute)
	if err := s.RevokeHost(ctx, owner.Token, owner.CSRF, host.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heartbeat(ctx, agent.serial, agent.credential.AgentID, "0.3.0", nil, now); !errors.Is(err, ErrCredentials) {
		t.Fatalf("revoked Agent accepted: %v", err)
	}
	if err := s.RevokeHost(ctx, owner.Token, owner.CSRF, host.ID, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked twice: %v", err)
	}
	if _, err := s.CreateEnrollment(ctx, owner.Token, owner.CSRF, host.ID, 10*time.Minute, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked host re-enrolled: %v", err)
	}
	if err := s.DeleteHost(ctx, owner.Token, owner.CSRF, host.ID, now); err != nil {
		t.Fatal(err)
	}
	if hosts, err := s.Hosts(ctx, owner.Token, now); err != nil || len(hosts) != 0 {
		t.Fatalf("deleted host listed: %v %v", hosts, err)
	}
	if err := s.DeleteHost(ctx, owner.Token, owner.CSRF, host.ID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted twice: %v", err)
	}
	var audits int
	if err := s.db.QueryRow("SELECT count(*) FROM audit_events WHERE host_id=? AND action IN ('agent.enroll','host.revoke','host.delete') AND outcome='success'", host.ID).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("trust audit kept after delete: %d %v", audits, err)
	}
}

func TestCredentialRenewalRetiresTheOldCredential(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	agent := enrollAgent(t, s, owner, host, ca)
	id := agent.credential.AgentID
	_, sameKey := func() (*ecdsa.PrivateKey, []byte) {
		der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, agent.key)
		if err != nil {
			t.Fatal(err)
		}
		return agent.key, der
	}()
	if _, err := s.RenewCredential(ctx, agent.serial, id, sameKey, ca, testTime); !errors.Is(err, ErrInput) {
		t.Fatalf("renewed with the same key: %v", err)
	}
	_, csr := agentKey(t)
	renewed, err := s.RenewCredential(ctx, agent.serial, id, csr, ca, testTime)
	if err != nil {
		t.Fatal(err)
	}
	// A lost response is retried with the same request and gets the same result.
	again, err := s.RenewCredential(ctx, agent.serial, id, csr, ca, testTime)
	if err != nil || again != renewed {
		t.Fatalf("renewal retry: %v", err)
	}
	_, otherCSR := agentKey(t)
	if _, err := s.RenewCredential(ctx, agent.serial, id, otherCSR, ca, testTime); !errors.Is(err, ErrConflict) {
		t.Fatalf("second successor: %v", err)
	}
	// The old credential still works during the grace period...
	if _, err := s.Heartbeat(ctx, agent.serial, id, "0.3.0", nil, testTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// ...and not after it.
	if _, err := s.Heartbeat(ctx, agent.serial, id, "0.3.0", nil, testTime.Add(credentialGrace)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("old credential after grace: %v", err)
	}
	next := serialOf(t, renewed.Certificate)
	if _, err := s.Heartbeat(ctx, next, id, "0.3.0", nil, testTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Using the successor retires the old credential at once.
	if _, err := s.Heartbeat(ctx, agent.serial, id, "0.3.0", nil, testTime.Add(time.Hour)); !errors.Is(err, ErrCredentials) {
		t.Fatalf("old credential after successor use: %v", err)
	}
}

func TestRestoredDatabaseTrustsNoAgent(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	agent := enrollAgent(t, s, owner, host, ca)
	pending, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "pending", testTime)
	if err != nil {
		t.Fatal(err)
	}
	unused, err := s.CreateEnrollment(ctx, owner.Token, owner.CSRF, pending.ID, 10*time.Minute, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareRestoredState(ctx, "restore-test", testTime, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heartbeat(ctx, agent.serial, agent.credential.AgentID, "0.3.0", nil, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("restored credential accepted: %v", err)
	}
	_, csr := agentKey(t)
	if _, err := s.Enroll(ctx, unused.Code, csr, ca, testTime); !errors.Is(err, ErrCredentials) {
		t.Fatalf("restored enrollment code accepted: %v", err)
	}
	// The host can enroll again with a new code and a new Agent ID.
	if hostStateOf(t, s, host.ID) != "pending" {
		t.Fatal("restored host is not pending")
	}
	owner, err = s.Login(ctx, "owner", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	again := enrollAgent(t, s, owner, host, ca)
	if again.credential.AgentID == agent.credential.AgentID {
		t.Fatal("old Agent ID reused")
	}
}
