// Package agentca issues the client certificates Agents use for mTLS.
//
// The CA key is separate from the server TLS key and from every Agent key.
// It is not part of backups: a restore removes it so a new CA is created and
// every Agent enrolls again (specification §14.5).
package agentca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	CertFile = "agent-ca.pem"
	KeyFile  = "agent-ca-key.pem"
	// CredentialLifetime is the validity of one Agent credential.
	CredentialLifetime = 30 * 24 * time.Hour
	uriPrefix          = "amuos://agent/"
)

var ErrInvalidRequest = errors.New("invalid certificate request")

type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

// Load reads the CA from dir, creating a new one when neither file exists.
// A half-present CA is an error rather than being silently replaced.
func Load(dir string, now time.Time) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile)
	_, certErr := os.Lstat(certPath)
	_, keyErr := os.Lstat(keyPath)
	if errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist) {
		if err := create(certPath, keyPath, now); err != nil {
			return nil, err
		}
	} else if certErr != nil || keyErr != nil {
		return nil, errors.New("agent CA is incomplete; remove both files to create a new one")
	}
	certPEM, err := readPrivate(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := readPrivate(keyPath)
	if err != nil {
		return nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" || keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		return nil, errors.New("invalid agent CA files")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, err
	}
	if !cert.IsCA || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("agent CA key does not match its certificate")
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &CA{cert: cert, key: key, pool: pool}, nil
}

// Remove deletes the CA files; the next Load creates a new CA.
func Remove(dir string) error {
	var errs []error
	for _, name := range []string{KeyFile, CertFile} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func readPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("agent CA file must be a regular file")
	}
	return os.ReadFile(path)
}

func create(certPath, keyPath string, now time.Time) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "AmuOS Agent CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	// The key is written first and both files are created exclusively.
	if err := writeNew(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return err
	}
	if err := writeNew(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		_ = os.Remove(keyPath)
		return err
	}
	return nil
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

// Pool returns the trust anchors for verifying Agent client certificates.
func (c *CA) Pool() *x509.CertPool { return c.pool }

// Credential is one issued Agent certificate.
type Credential struct {
	Serial        string
	CertPEM       string
	PublicKeyHash []byte
	NotAfter      time.Time
}

// ParseRequest checks a PKCS#10 request: its signature proves possession of
// the private key, which must be ECDSA P-256. Everything else in the request
// (subject, extensions) is ignored; identity comes from the server.
func ParseRequest(der []byte) (*ecdsa.PublicKey, error) {
	request, err := x509.ParseCertificateRequest(der)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if err := request.CheckSignature(); err != nil {
		return nil, ErrInvalidRequest
	}
	key, ok := request.PublicKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, ErrInvalidRequest
	}
	return key, nil
}

// KeyHash identifies a public key without storing more than needed.
func KeyHash(key *ecdsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(der)
	return sum[:], nil
}

// Issue signs a client certificate for agentID. The serial is random and the
// only identity the certificate carries is the Agent ID as a URI SAN.
func (c *CA) Issue(key *ecdsa.PublicKey, agentID string, now time.Time) (Credential, error) {
	serial, err := randomSerial()
	if err != nil {
		return Credential{}, err
	}
	hash, err := KeyHash(key)
	if err != nil {
		return Credential{}, err
	}
	uri, err := url.Parse(uriPrefix + agentID)
	if err != nil {
		return Credential{}, err
	}
	notAfter := now.Add(CredentialLifetime)
	if notAfter.After(c.cert.NotAfter) {
		return Credential{}, errors.New("agent CA expires before the credential")
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "amuos-agent"},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{uri},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, c.cert, key, c.key)
	if err != nil {
		return Credential{}, err
	}
	return Credential{
		Serial:        SerialString(serial),
		CertPEM:       string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		PublicKeyHash: hash,
		NotAfter:      notAfter,
	}, nil
}

func SerialString(serial *big.Int) string { return hex.EncodeToString(serial.Bytes()) }

// Identity extracts the serial and Agent ID from a verified client
// certificate. The database decides whether they are still trusted.
func Identity(cert *x509.Certificate) (serial, agentID string, err error) {
	if len(cert.URIs) != 1 || !strings.HasPrefix(cert.URIs[0].String(), uriPrefix) {
		return "", "", fmt.Errorf("agent certificate has no agent identity")
	}
	agentID = strings.TrimPrefix(cert.URIs[0].String(), uriPrefix)
	if agentID == "" || strings.ContainsAny(agentID, "/?#") {
		return "", "", fmt.Errorf("agent certificate has an invalid agent identity")
	}
	return SerialString(cert.SerialNumber), agentID, nil
}

// RecoverDigest is what an Agent signs with its enrollment key to fetch an
// enrollment result it did not receive. It binds the enrollment, the one-time
// challenge and its nonce, so a signature cannot be reused for another request.
func RecoverDigest(enrollmentID, challengeID, nonce string) []byte {
	sum := sha256.Sum256([]byte("amuos-agent-recover-v1\x00" + enrollmentID + "\x00" + challengeID + "\x00" + nonce))
	return sum[:]
}
