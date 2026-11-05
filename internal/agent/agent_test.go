package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/controlplane"
	"github.com/sudatti624/AmuOS/internal/store"
)

const password = "correct horse battery staple"

func pemKey(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

// serverCertificate is a self-signed certificate for 127.0.0.1, test only.
func serverCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "control-plane"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

type controlPlane struct {
	db        *store.Store
	ca        *agentca.CA
	owner     store.Session
	roots     *x509.CertPool
	public    *httptest.Server
	agents    *httptest.Server
	stateRoot string
}

func startControlPlane(t *testing.T) *controlPlane {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "amuos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	token, err := db.PrepareBootstrap(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Setup(ctx, token, "owner", password, now); err != nil {
		t.Fatal(err)
	}
	owner, err := db.Login(ctx, "owner", password, now)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := agentca.Load(root, now)
	if err != nil {
		t.Fatal(err)
	}
	public, agentHandler, err := controlplane.Handlers(db, "https://amuos.test", ca)
	if err != nil {
		t.Fatal(err)
	}
	certificate, roots := serverCertificate(t)
	cp := &controlPlane{db: db, ca: ca, owner: owner, roots: roots, stateRoot: root}
	cp.public = httptest.NewUnstartedServer(public)
	cp.public.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}}
	cp.public.StartTLS()
	t.Cleanup(cp.public.Close)
	cp.agents = httptest.NewUnstartedServer(agentHandler)
	cp.agents.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: ca.Pool()}
	cp.agents.StartTLS()
	t.Cleanup(cp.agents.Close)
	return cp
}

func (cp *controlPlane) code(t *testing.T) (store.Host, string) {
	t.Helper()
	ctx := context.Background()
	host, err := cp.db.CreateHost(ctx, cp.owner.Token, cp.owner.CSRF, "managed", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := cp.db.CreateEnrollment(ctx, cp.owner.Token, cp.owner.CSRF, host.ID, 10*time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return host, enrollment.Code
}

func (cp *controlPlane) agent(t *testing.T, dir string) *Agent {
	t.Helper()
	a, err := New(Config{StateDir: dir, Server: cp.public.URL, AgentServer: cp.agents.URL, Roots: cp.roots, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestEnrollHeartbeatRenewAndRevoke(t *testing.T) {
	cp := startControlPlane(t)
	ctx := context.Background()
	host, code := cp.code(t)
	a := cp.agent(t, t.TempDir())
	hostID, err := a.Enroll(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if hostID != host.ID {
		t.Fatalf("enrolled for %s, want %s", hostID, host.ID)
	}
	if _, err := a.Enroll(ctx, code); err == nil {
		t.Fatal("enrolled twice")
	}
	if err := a.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	hosts, err := cp.db.Hosts(ctx, cp.owner.Token, time.Now())
	if err != nil || hosts[0].Agent == nil || !hosts[0].Agent.Connected || hosts[0].Agent.Version != "test" {
		t.Fatalf("heartbeat not recorded: %+v %v", hosts, err)
	}
	// Renewal is due 10 days before expiry.
	_, before, _, err := a.loadCredential()
	if err != nil {
		t.Fatal(err)
	}
	a.clock = func() time.Time { return before.NotAfter.Add(-9 * 24 * time.Hour) }
	if err := a.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	a.clock = time.Now
	_, after, _, err := a.loadCredential()
	if err != nil {
		t.Fatal(err)
	}
	if after.SerialNumber.Cmp(before.SerialNumber) == 0 {
		t.Fatal("credential was not renewed")
	}
	if _, err := os.Stat(filepath.Join(a.cfg.StateDir, nextFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("renewal key left behind")
	}
	if err := a.Beat(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cp.db.RevokeHost(ctx, cp.owner.Token, cp.owner.CSRF, host.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := a.Beat(ctx); !errors.Is(err, ErrRejected) {
		t.Fatalf("revoked Agent: %v", err)
	}
}

func TestLostEnrollmentResponseIsRecoveredWithTheSameKey(t *testing.T) {
	cp := startControlPlane(t)
	ctx := context.Background()
	host, code := cp.code(t)
	dir := t.TempDir()
	a := cp.agent(t, dir)
	if _, err := a.Enroll(ctx, code); err != nil {
		t.Fatal(err)
	}
	// Simulate a response lost after the server committed: the Agent kept
	// only its pending key.
	bundle, err := readPrivate(filepath.Join(dir, credentialFile))
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := parseKey(bundle)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalECPrivateKey(key)
	lost := t.TempDir()
	enrollmentID, _, _ := strings.Cut(code, ".")
	if err := writeAtomic(filepath.Join(lost, pendingFile), append(pemKey(der), []byte(enrollmentID+"\n")...)); err != nil {
		t.Fatal(err)
	}
	hostID, err := cp.agent(t, lost).Enroll(ctx, code)
	if err != nil || hostID != host.ID {
		t.Fatalf("recovery: %s %v", hostID, err)
	}
	// Another key cannot recover the result with the same code.
	if _, err := cp.agent(t, t.TempDir()).Enroll(ctx, code); err == nil {
		t.Fatal("another key recovered the enrollment")
	}
}

func TestTLSRejectsUntrustedPeers(t *testing.T) {
	cp := startControlPlane(t)
	ctx := context.Background()
	// An Agent that does not trust the server's certificate never connects.
	_, otherRoots := serverCertificate(t)
	_, code := cp.code(t)
	untrusting, err := New(Config{StateDir: t.TempDir(), Server: cp.public.URL, AgentServer: cp.agents.URL, Roots: otherRoots})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := untrusting.Enroll(ctx, code); err == nil {
		t.Fatal("enrolled with an untrusted server")
	}
	// A client certificate from another CA is refused during the handshake.
	forgedCA, err := agentca.Load(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := forgedCA.Issue(&key.PublicKey, "forged", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalECPrivateKey(key)
	certificate, err := tls.X509KeyPair([]byte(forged.CertPEM), pemKey(der))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: cp.roots, Certificates: []tls.Certificate{certificate}}}}
	if err := post(ctx, client, cp.agents.URL+"/agent/v1/heartbeat", map[string]any{"version": "x"}, nil); err == nil {
		t.Fatal("forged certificate accepted")
	}
	noCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: cp.roots}}}
	if err := post(ctx, noCert, cp.agents.URL+"/agent/v1/heartbeat", map[string]any{"version": "x"}, nil); err == nil {
		t.Fatal("request without a client certificate accepted")
	}
}
