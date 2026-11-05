package agentca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func request(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "spoofed-host"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestLoadCreatesOnceAndRejectsHalfPresentCA(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	first, err := Load(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Load(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if !first.cert.Equal(again.cert) {
		t.Fatal("existing CA was replaced")
	}
	info, err := os.Stat(filepath.Join(dir, KeyFile))
	if err != nil || (info.Mode().Perm()&0077 != 0 && os.PathSeparator == '/') {
		t.Fatalf("CA key permissions: %v %v", info, err)
	}
	if err := os.Remove(filepath.Join(dir, CertFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, now); err == nil {
		t.Fatal("half-present CA was accepted or replaced")
	}
	if err := Remove(dir); err != nil {
		t.Fatal(err)
	}
	renewed, err := Load(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.cert.Equal(first.cert) {
		t.Fatal("removed CA came back")
	}
}

func TestIssueBindsOnlyTheServerChosenIdentity(t *testing.T) {
	now := time.Now()
	ca, err := Load(t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := ParseRequest(request(t, key))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := ca.Issue(public, "agent-1", now)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(credential.CertPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: ca.Pool(), CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
	// The requester's subject is ignored.
	if cert.Subject.CommonName != "amuos-agent" || len(cert.DNSNames) != 0 {
		t.Fatalf("certificate kept requester data: %v", cert.Subject)
	}
	serial, agentID, err := Identity(cert)
	if err != nil || agentID != "agent-1" || serial != credential.Serial {
		t.Fatalf("identity %q %q %v", serial, agentID, err)
	}
	if got := cert.NotAfter.Sub(now); got < CredentialLifetime-time.Minute || got > CredentialLifetime+time.Minute {
		t.Fatalf("lifetime %v", got)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: ca.Pool(), CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Fatal("agent certificate is valid for server authentication")
	}
}

func TestParseRequestRejectsWeakOrForgedRequests(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRequest(request(t, rsaKey)); err == nil {
		t.Fatal("accepted an RSA key")
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRequest(request(t, p384)); err == nil {
		t.Fatal("accepted a P-384 key")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged := request(t, key)
	forged[len(forged)-3] ^= 0xff
	if _, err := ParseRequest(forged); err == nil {
		t.Fatal("accepted a request with a broken signature")
	}
	if _, err := ParseRequest([]byte("garbage")); err == nil {
		t.Fatal("accepted garbage")
	}
}
