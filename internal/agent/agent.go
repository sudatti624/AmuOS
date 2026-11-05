// Package agent is the AmuOS Agent: it enrolls once with a one-time code,
// then talks to the Control Plane only over mTLS.
package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/metrics"
	"github.com/sudatti624/AmuOS/internal/security"
)

const (
	identityFile    = "agent.json"
	credentialFile  = "credential.pem"
	pendingFile     = "enrollment.pem"
	nextFile        = "renewal.pem"
	defaultInterval = 15 * time.Second
	// Unsent samples are kept for at most this long and this many bytes.
	maxBufferAge   = 10 * time.Minute
	maxBufferBytes = 10 << 20
	infoEvery      = 10 * time.Minute
	// Half the server limit keeps a batch of long numbers well under 64 KiB.
	sampleBatch = metrics.MaxBatch / 2
	maxBackoff  = 60 * time.Second
	maxResponse = 256 * 1024
)

// ErrRejected means the Control Plane no longer trusts this Agent (revoked,
// deleted, expired or restored). Only a new enrollment recovers from it.
var ErrRejected = errors.New("the control plane rejected this agent's credential; enroll again")

type Config struct {
	StateDir string
	// Server is the browser/enrollment endpoint, e.g. https://cp.example:8443.
	Server string
	// AgentServer is the mTLS endpoint, e.g. https://cp.example:9443.
	AgentServer string
	// Roots verify the Control Plane's TLS certificate. There is no option to
	// skip verification.
	Roots   *x509.CertPool
	Version string
	// Collector reads the host; nil uses the platform collector.
	Collector Collector
}

type identity struct {
	AgentID      string `json:"agent_id"`
	HostID       string `json:"host_id"`
	EnrollmentID string `json:"enrollment_id"`
}

type Agent struct {
	cfg   Config
	clock func() time.Time

	// Samples of this run wait in memory until the Control Plane accepts
	// them. A restart starts a new run, so the server never confuses the
	// sequence numbers of two runs.
	runID       string
	seq         int64
	pending     []pendingSample
	pendingSize int
	dropped     int
	supported   bool
	nextInfo    time.Time
	interval    time.Duration
}

type pendingSample struct {
	sample metrics.Sample
	size   int
}

func New(cfg Config) (*Agent, error) {
	if cfg.StateDir == "" || cfg.Roots == nil || !strings.HasPrefix(cfg.Server, "https://") || !strings.HasPrefix(cfg.AgentServer, "https://") {
		return nil, errors.New("state directory, HTTPS endpoints and trusted roots are required")
	}
	if cfg.Collector == nil {
		cfg.Collector = NewCollector()
	}
	return &Agent{cfg: cfg, clock: time.Now, runID: security.Token(), supported: true, interval: defaultInterval}, nil
}

func (a *Agent) path(name string) string { return filepath.Join(a.cfg.StateDir, name) }

// writeAtomic replaces a private file so a crash leaves the old or new
// content, never a mix.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func readPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", filepath.Base(path))
	}
	if os.PathSeparator == '/' && info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("%s must be accessible only by its owner", filepath.Base(path))
	}
	return os.ReadFile(path)
}

func newKey() (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func parseKey(data []byte) (*ecdsa.PrivateKey, []byte, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, nil, errors.New("invalid private key file")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	return key, rest, err
}

func certificateRequest(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	return base64.StdEncoding.EncodeToString(der), err
}

func (a *Agent) client(certificate *tls.Certificate) *http.Client {
	config := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: a.cfg.Roots}
	if certificate != nil {
		config.Certificates = []tls.Certificate{*certificate}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: config, ForceAttemptHTTP2: true}}
}

type statusError struct {
	status int
	code   string
}

func (e statusError) Error() string {
	return fmt.Sprintf("control plane answered %d %s", e.status, e.code)
}

func post(ctx context.Context, client *http.Client, url string, body, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		return statusError{response.StatusCode, failure.Error}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

type credentialResponse struct {
	AgentID     string `json:"agent_id"`
	HostID      string `json:"host_id"`
	Certificate string `json:"certificate"`
	NotAfter    int64  `json:"not_after"`
}

// Enroll binds this Agent to the host its one-time code was issued for. The
// key is created and saved before anything is sent, so a lost response is
// recovered by proving possession of that key, never by reusing the code.
func (a *Agent) Enroll(ctx context.Context, code string) (string, error) {
	if _, err := os.Lstat(a.path(identityFile)); err == nil {
		return "", errors.New("this agent is already enrolled; remove its state directory to enroll again")
	}
	enrollmentID, _, ok := strings.Cut(code, ".")
	if !ok || enrollmentID == "" {
		return "", errors.New("invalid enrollment code")
	}
	var key *ecdsa.PrivateKey
	data, err := readPrivate(a.path(pendingFile))
	switch {
	case err == nil:
		var rest []byte
		if key, rest, err = parseKey(data); err != nil {
			return "", err
		}
		if strings.TrimSpace(string(rest)) != enrollmentID {
			return "", errors.New("an enrollment with another code is in progress; remove enrollment.pem to start over")
		}
	case errors.Is(err, os.ErrNotExist):
		var keyPEM []byte
		if key, keyPEM, err = newKey(); err != nil {
			return "", err
		}
		if err := writeAtomic(a.path(pendingFile), append(keyPEM, []byte(enrollmentID+"\n")...)); err != nil {
			return "", err
		}
	default:
		return "", err
	}
	csr, err := certificateRequest(key)
	if err != nil {
		return "", err
	}
	client := a.client(nil)
	var result credentialResponse
	err = post(ctx, client, a.cfg.Server+"/api/v1/agent/enroll", map[string]string{"code": code, "csr": csr}, &result)
	var status statusError
	if errors.As(err, &status) && status.status == http.StatusConflict {
		// The code was used, perhaps by us before a lost response.
		err = a.recover(ctx, client, enrollmentID, key, &result)
	}
	if err != nil {
		return "", err
	}
	if err := a.saveCredential(key, result, enrollmentID); err != nil {
		return "", err
	}
	_ = os.Remove(a.path(pendingFile))
	return result.HostID, nil
}

func (a *Agent) recover(ctx context.Context, client *http.Client, enrollmentID string, key *ecdsa.PrivateKey, result *credentialResponse) error {
	var challenge struct {
		ID    string `json:"challenge_id"`
		Nonce string `json:"nonce"`
	}
	if err := post(ctx, client, a.cfg.Server+"/api/v1/agent/enrollments/"+enrollmentID+"/challenge", struct{}{}, &challenge); err != nil {
		return err
	}
	signature, err := ecdsa.SignASN1(rand.Reader, key, agentca.RecoverDigest(enrollmentID, challenge.ID, challenge.Nonce))
	if err != nil {
		return err
	}
	return post(ctx, client, a.cfg.Server+"/api/v1/agent/enrollments/"+enrollmentID+"/recover", map[string]string{
		"challenge_id": challenge.ID, "nonce": challenge.Nonce, "signature": base64.StdEncoding.EncodeToString(signature),
	}, result)
}

func (a *Agent) saveCredential(key *ecdsa.PrivateKey, result credentialResponse, enrollmentID string) error {
	block, _ := pem.Decode([]byte(result.Certificate))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("control plane returned an invalid certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return errors.New("control plane returned a certificate for another key")
	}
	if _, agentID, err := agentca.Identity(cert); err != nil || agentID != result.AgentID {
		return errors.New("control plane returned a certificate for another agent")
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	bundle := append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), []byte(result.Certificate)...)
	if err := writeAtomic(a.path(credentialFile), bundle); err != nil {
		return err
	}
	if enrollmentID == "" {
		return nil
	}
	encoded, err := json.Marshal(identity{AgentID: result.AgentID, HostID: result.HostID, EnrollmentID: enrollmentID})
	if err != nil {
		return err
	}
	return writeAtomic(a.path(identityFile), encoded)
}

func (a *Agent) loadCredential() (tls.Certificate, *x509.Certificate, identity, error) {
	var id identity
	data, err := readPrivate(a.path(identityFile))
	if err != nil {
		return tls.Certificate{}, nil, id, fmt.Errorf("agent is not enrolled: %w", err)
	}
	if err := json.Unmarshal(data, &id); err != nil {
		return tls.Certificate{}, nil, id, err
	}
	bundle, err := readPrivate(a.path(credentialFile))
	if err != nil {
		return tls.Certificate{}, nil, id, err
	}
	certificate, err := tls.X509KeyPair(bundle, bundle)
	if err != nil {
		return tls.Certificate{}, nil, id, err
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tls.Certificate{}, nil, id, err
	}
	return certificate, leaf, id, nil
}

type heartbeatResponse struct {
	RenewAfter     int64 `json:"renew_after"`
	SampleInterval int64 `json:"sample_interval"`
}

// Beat sends one heartbeat and renews the credential when it is due.
func (a *Agent) Beat(ctx context.Context) error {
	certificate, leaf, id, err := a.loadCredential()
	if err != nil {
		return err
	}
	if !a.clock().Before(leaf.NotAfter) {
		return ErrRejected
	}
	var result heartbeatResponse
	err = post(ctx, a.client(&certificate), a.cfg.AgentServer+"/agent/v1/heartbeat", map[string]any{"version": a.cfg.Version, "capabilities": a.capabilities()}, &result)
	var status statusError
	if errors.As(err, &status) && status.status == http.StatusUnauthorized {
		return ErrRejected
	}
	if err != nil {
		return err
	}
	if result.SampleInterval >= 5 && result.SampleInterval <= 300 {
		a.interval = time.Duration(result.SampleInterval) * time.Second
	}
	if result.RenewAfter > 0 && a.clock().Unix() >= result.RenewAfter {
		return a.renew(ctx, certificate, id)
	}
	return nil
}

// renew keeps the new key on disk before asking, so a lost response is
// retried with the same key and the server returns the same successor.
func (a *Agent) renew(ctx context.Context, current tls.Certificate, id identity) error {
	var key *ecdsa.PrivateKey
	data, err := readPrivate(a.path(nextFile))
	switch {
	case err == nil:
		if key, _, err = parseKey(data); err != nil {
			return err
		}
		// A crash after saving the successor leaves its key here; never reuse it.
		if leaf, parseErr := x509.ParseCertificate(current.Certificate[0]); parseErr == nil && key.PublicKey.Equal(leaf.PublicKey) {
			if err := os.Remove(a.path(nextFile)); err != nil {
				return err
			}
			return a.renew(ctx, current, id)
		}
	case errors.Is(err, os.ErrNotExist):
		var keyPEM []byte
		if key, keyPEM, err = newKey(); err != nil {
			return err
		}
		if err := writeAtomic(a.path(nextFile), keyPEM); err != nil {
			return err
		}
	default:
		return err
	}
	csr, err := certificateRequest(key)
	if err != nil {
		return err
	}
	var result credentialResponse
	if err := post(ctx, a.client(&current), a.cfg.AgentServer+"/agent/v1/credentials/renew", map[string]string{"csr": csr}, &result); err != nil {
		return fmt.Errorf("renew credential: %w", err)
	}
	if result.AgentID != id.AgentID {
		return errors.New("control plane renewed another agent's credential")
	}
	if err := a.saveCredential(key, result, ""); err != nil {
		return err
	}
	slog.Info("agent credential renewed", "not_after", time.Unix(result.NotAfter, 0).UTC())
	return os.Remove(a.path(nextFile))
}

// jitter returns a random duration in [d/2, d].
func jitter(d time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(d/2)+1))
	if err != nil {
		return d
	}
	return d/2 + time.Duration(n.Int64())
}

// Run sends heartbeats until ctx ends. Failures back off exponentially from
// 1 to 60 seconds with jitter. A rejected credential stops the Agent.
// Collection keeps its own interval while the Control Plane is unreachable.
func (a *Agent) Run(ctx context.Context) error {
	backoff := time.Second
	var retryAt time.Time
	for {
		now := a.clock()
		a.collect(now)
		if !now.Before(retryAt) {
			if err := a.sync(ctx); err != nil {
				if errors.Is(err, ErrRejected) || ctx.Err() != nil {
					return err
				}
				slog.Warn("heartbeat failed", "error", err)
				retryAt, backoff = now.Add(jitter(backoff)), min(backoff*2, maxBackoff)
			} else {
				retryAt, backoff = time.Time{}, time.Second
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(a.interval):
		}
	}
}

func (a *Agent) capabilities() []string {
	if a.supported {
		return []string{"heartbeat", "metrics", "host_info"}
	}
	return []string{"heartbeat"}
}

// collect takes one sample into the unsent buffer, then drops the oldest
// samples beyond the age and size limits. Dropped samples leave a gap in the
// sequence numbers, which the history shows as missing.
func (a *Agent) collect(now time.Time) {
	if !a.supported {
		return
	}
	sample, err := a.cfg.Collector.Sample(now)
	if errors.Is(err, ErrUnsupported) {
		a.supported = false
		slog.Warn("metrics are not collected on this platform; only heartbeats are sent")
		return
	}
	if err != nil {
		slog.Warn("metrics collection failed", "error", err)
		return
	}
	a.seq++
	sample.Seq = a.seq
	encoded, err := json.Marshal(sample)
	if err != nil {
		return
	}
	a.pending = append(a.pending, pendingSample{sample, len(encoded)})
	a.pendingSize += len(encoded)
	cutoff := now.Add(-maxBufferAge).Unix()
	drop := 0
	for drop < len(a.pending) && (a.pending[drop].sample.CollectedAt < cutoff || a.pendingSize > maxBufferBytes) {
		a.pendingSize -= a.pending[drop].size
		drop++
	}
	if drop > 0 {
		a.pending = append([]pendingSample(nil), a.pending[drop:]...)
		a.dropped += drop
		slog.Warn("unsent samples dropped", "count", drop, "total", a.dropped)
	}
}

// sync sends a heartbeat, then the unsent samples and, when due, host
// information. A batch the Control Plane refuses as invalid is dropped so
// it cannot block newer samples.
func (a *Agent) sync(ctx context.Context) error {
	if err := a.Beat(ctx); err != nil {
		return err
	}
	certificate, _, _, err := a.loadCredential()
	if err != nil {
		return err
	}
	client := a.client(&certificate)
	for len(a.pending) > 0 {
		n := min(len(a.pending), sampleBatch)
		batch := make([]metrics.Sample, n)
		for i := range batch {
			batch[i] = a.pending[i].sample
		}
		err := post(ctx, client, a.cfg.AgentServer+"/agent/v1/samples", map[string]any{"run_id": a.runID, "samples": batch}, nil)
		if invalid(err) {
			slog.Warn("control plane refused a sample batch; dropping it", "error", err, "count", n)
		} else if err != nil {
			return fmt.Errorf("send samples: %w", err)
		}
		for _, p := range a.pending[:n] {
			a.pendingSize -= p.size
		}
		a.pending = append([]pendingSample(nil), a.pending[n:]...)
	}
	now := a.clock()
	if !a.supported || now.Before(a.nextInfo) {
		return nil
	}
	info, err := a.cfg.Collector.HostInfo(now)
	if err != nil {
		slog.Warn("host information collection failed", "error", err)
		a.nextInfo = now.Add(infoEvery)
		return nil
	}
	err = post(ctx, client, a.cfg.AgentServer+"/agent/v1/host-info", info, nil)
	if invalid(err) {
		slog.Warn("control plane refused host information", "error", err)
	} else if err != nil {
		return fmt.Errorf("send host information: %w", err)
	}
	a.nextInfo = now.Add(infoEvery)
	return nil
}

func invalid(err error) bool {
	var status statusError
	return errors.As(err, &status) && (status.status == http.StatusBadRequest || status.status == http.StatusRequestEntityTooLarge)
}
