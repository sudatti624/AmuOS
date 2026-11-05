package store

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

// Issuer signs Agent credentials. The CA lives outside the database.
type Issuer interface {
	Issue(key *ecdsa.PublicKey, agentID string, now time.Time) (agentca.Credential, error)
}

type Enrollment struct {
	ID        string `json:"id"`
	HostID    string `json:"host_id"`
	Code      string `json:"code"`
	ExpiresAt int64  `json:"expires_at"`
}

// AgentCredential is returned to an Agent; it contains no secret.
type AgentCredential struct {
	AgentID     string `json:"agent_id"`
	HostID      string `json:"host_id"`
	Certificate string `json:"certificate"`
	NotAfter    int64  `json:"not_after"`
}

// AgentInfo is what users see about a host's Agent.
type AgentInfo struct {
	ID                   string   `json:"id"`
	Version              string   `json:"version"`
	Capabilities         []string `json:"capabilities"`
	LastSeen             *int64   `json:"last_seen"`
	Connected            bool     `json:"connected"`
	CredentialExpiresAt  int64    `json:"credential_expires_at"`
	CredentialRenewalDue bool     `json:"credential_renewal_due"`
}

const (
	enrollmentMaxTTL  = 30 * time.Minute
	challengeLifetime = 30 * time.Second
	// A replaced credential stays usable for at most this long, until the
	// Agent is seen with its successor.
	credentialGrace = 24 * time.Hour
	renewalWindow   = 10 * 24 * time.Hour
	// A host is disconnected when nothing arrived for this long (§5.3).
	disconnectedAfter = 60 * time.Second
)

func (s *Store) CreateEnrollment(ctx context.Context, token, csrf, hostID string, ttl time.Duration, now time.Time) (Enrollment, error) {
	if ttl < time.Minute || ttl > enrollmentMaxTTL {
		return Enrollment{}, ErrInput
	}
	var result Enrollment
	var rejected error
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, hash, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !security.EqualDigest(csrf, hash) {
			rejected = ErrDenied
			return audit(ctx, tx, now, p.ID, "enrollment.create", "", "", "denied", "invalid_csrf")
		}
		state, err := hostState(tx, p, hostID)
		if err != nil {
			return err
		}
		if state == "" {
			rejected = ErrNotFound
			return audit(ctx, tx, now, p.ID, "enrollment.create", "", "", "denied", "not_found")
		}
		if !p.Allows(rbac.HostManage, hostID) {
			rejected = ErrDenied
			return audit(ctx, tx, now, p.ID, "enrollment.create", hostID, hostID, "denied", "forbidden")
		}
		if state != "pending" {
			rejected = ErrConflict
			return audit(ctx, tx, now, p.ID, "enrollment.create", hostID, hostID, "denied", "host_"+state)
		}
		// Issuing a new code invalidates every earlier unused one for the host.
		if _, err := tx.Exec("UPDATE enrollments SET revoked_at=? WHERE host_id=? AND consumed_at IS NULL AND revoked_at IS NULL", now.Unix(), hostID); err != nil {
			return err
		}
		secret := security.Token()
		result = Enrollment{ID: security.Token(), HostID: hostID, ExpiresAt: now.Add(ttl).Unix()}
		result.Code = result.ID + "." + secret
		if _, err := tx.Exec("INSERT INTO enrollments(id,host_id,token_hash,issuer_id,created_at,expires_at) VALUES(?,?,?,?,?,?)", result.ID, hostID, security.Digest(secret), p.ID, now.Unix(), result.ExpiresAt); err != nil {
			return err
		}
		return auditWithDetails(ctx, tx, now, p.ID, "enrollment.create", result.ID, hostID, "success", "", map[string]any{"expires_at": result.ExpiresAt})
	})
	if err != nil {
		return Enrollment{}, err
	}
	if rejected != nil {
		return Enrollment{}, rejected
	}
	return result, nil
}

func splitCode(code string) (id, secret string, ok bool) {
	id, secret, ok = strings.Cut(code, ".")
	return id, secret, ok && id != "" && secret != "" && len(code) <= 256
}

// Enroll consumes a one-time code and binds a new Agent ID and the requester's
// key to the code's host, in one transaction. The host comes from the code,
// never from the Agent. A consumed code is a conflict: the Agent then
// recovers the original result by proving it holds the same key.
func (s *Store) Enroll(ctx context.Context, code string, request []byte, issuer Issuer, now time.Time) (AgentCredential, error) {
	id, secret, ok := splitCode(code)
	if !ok {
		return AgentCredential{}, ErrCredentials
	}
	key, err := agentca.ParseRequest(request)
	if err != nil {
		return AgentCredential{}, ErrInput
	}
	publicKey, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return AgentCredential{}, err
	}
	var result AgentCredential
	var rejected error
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var hostID, hostState string
		var tokenHash []byte
		var expires int64
		var consumed, revoked sql.NullInt64
		err := tx.QueryRow("SELECT e.host_id,e.token_hash,e.expires_at,e.consumed_at,e.revoked_at,h.state FROM enrollments e JOIN hosts h ON h.id=e.host_id WHERE e.id=?", id).Scan(&hostID, &tokenHash, &expires, &consumed, &revoked, &hostState)
		if errors.Is(err, sql.ErrNoRows) {
			rejected = ErrCredentials
			return audit(ctx, tx, now, "anonymous", "agent.enroll", "", "", "denied", "invalid_code")
		}
		if err != nil {
			return err
		}
		switch {
		case !security.EqualDigest(secret, tokenHash):
			rejected = ErrCredentials
			return audit(ctx, tx, now, "anonymous", "agent.enroll", id, hostID, "denied", "invalid_code")
		case consumed.Valid:
			rejected = ErrConflict
			return audit(ctx, tx, now, "anonymous", "agent.enroll", id, hostID, "denied", "code_consumed")
		case revoked.Valid || now.Unix() >= expires:
			rejected = ErrCredentials
			return audit(ctx, tx, now, "anonymous", "agent.enroll", id, hostID, "denied", "code_expired_or_revoked")
		case hostState != "pending":
			rejected = ErrCredentials
			return audit(ctx, tx, now, "anonymous", "agent.enroll", id, hostID, "denied", "host_"+hostState)
		}
		agentID := security.Token()
		credential, err := issuer.Issue(key, agentID, now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO agents(id,host_id,public_key_hash,state,created_at) VALUES(?,?,?,'active',?)", agentID, hostID, credential.PublicKeyHash, now.Unix()); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO agent_credentials(serial,agent_id,public_key_hash,cert_pem,issued_at,not_after) VALUES(?,?,?,?,?,?)", credential.Serial, agentID, credential.PublicKeyHash, credential.CertPEM, now.Unix(), credential.NotAfter.Unix()); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE enrollments SET consumed_at=?,public_key=?,agent_id=? WHERE id=? AND consumed_at IS NULL", now.Unix(), publicKey, agentID, id); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE hosts SET state='active' WHERE id=? AND state='pending'", hostID); err != nil {
			return err
		}
		result = AgentCredential{AgentID: agentID, HostID: hostID, Certificate: credential.CertPEM, NotAfter: credential.NotAfter.Unix()}
		return auditWithDetails(ctx, tx, now, agentID, "agent.enroll", agentID, hostID, "success", "", map[string]any{"enrollment_id": id, "serial": credential.Serial, "not_after": result.NotAfter})
	})
	if err != nil {
		return AgentCredential{}, err
	}
	if rejected != nil {
		return AgentCredential{}, rejected
	}
	return result, nil
}

// EnrollmentChallenge issues a 30-second, one-time nonce for a consumed
// enrollment. It reveals nothing; only a holder of the enrolled key can use it.
func (s *Store) EnrollmentChallenge(ctx context.Context, enrollmentID string, now time.Time) (challengeID, nonce string, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM enrollment_challenges WHERE expires_at<=?", now.Unix()); err != nil {
			return err
		}
		var consumed sql.NullInt64
		err := tx.QueryRow("SELECT consumed_at FROM enrollments WHERE id=?", enrollmentID).Scan(&consumed)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && !consumed.Valid) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		challengeID, nonce = security.Token(), security.Token()
		_, err = tx.Exec("INSERT INTO enrollment_challenges(id,enrollment_id,nonce_hash,expires_at) VALUES(?,?,?,?)", challengeID, enrollmentID, security.Digest(nonce), now.Add(challengeLifetime).Unix())
		return err
	})
	return challengeID, nonce, err
}

// RecoverEnrollment returns the original enrollment result to the holder of
// the enrolled key. Revoked or deleted Agents, and replaced or expired
// credentials, are refused: the Agent must then enroll again.
func (s *Store) RecoverEnrollment(ctx context.Context, enrollmentID, challengeID, nonce string, signature []byte, now time.Time) (AgentCredential, error) {
	var result AgentCredential
	var rejected error
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var nonceHash []byte
		var expires int64
		var consumed sql.NullInt64
		err := tx.QueryRow("SELECT nonce_hash,expires_at,consumed_at FROM enrollment_challenges WHERE id=? AND enrollment_id=?", challengeID, enrollmentID).Scan(&nonceHash, &expires, &consumed)
		if errors.Is(err, sql.ErrNoRows) {
			rejected = ErrCredentials
			return audit(ctx, tx, now, "anonymous", "agent.recover", enrollmentID, "", "denied", "invalid_challenge")
		}
		if err != nil {
			return err
		}
		// The challenge is spent by any attempt, valid or not.
		if _, err := tx.Exec("UPDATE enrollment_challenges SET consumed_at=? WHERE id=? AND consumed_at IS NULL", now.Unix(), challengeID); err != nil {
			return err
		}
		if consumed.Valid || now.Unix() >= expires || !security.EqualDigest(nonce, nonceHash) {
			rejected = ErrCredentials
			return audit(ctx, tx, now, "anonymous", "agent.recover", enrollmentID, "", "denied", "invalid_challenge")
		}
		var publicKey []byte
		var agentID, hostID, agentState, hostState string
		err = tx.QueryRow("SELECT e.public_key,e.agent_id,a.host_id,a.state,h.state FROM enrollments e JOIN agents a ON a.id=e.agent_id JOIN hosts h ON h.id=a.host_id WHERE e.id=?", enrollmentID).Scan(&publicKey, &agentID, &hostID, &agentState, &hostState)
		if err != nil {
			return err
		}
		parsed, err := x509.ParsePKIXPublicKey(publicKey)
		key, ok := parsed.(*ecdsa.PublicKey)
		if err != nil || !ok || !ecdsa.VerifyASN1(key, agentca.RecoverDigest(enrollmentID, challengeID, nonce), signature) {
			rejected = ErrCredentials
			return audit(ctx, tx, now, "anonymous", "agent.recover", agentID, hostID, "denied", "invalid_signature")
		}
		if agentState != "active" || hostState != "active" {
			rejected = ErrCredentials
			return audit(ctx, tx, now, agentID, "agent.recover", agentID, hostID, "denied", "agent_"+agentState)
		}
		keyHash, err := agentca.KeyHash(key)
		if err != nil {
			return err
		}
		var notAfter int64
		err = tx.QueryRow("SELECT cert_pem,not_after FROM agent_credentials WHERE agent_id=? AND public_key_hash=? AND replaces IS NULL AND revoked_at IS NULL AND retire_at IS NULL AND not_after>?", agentID, keyHash, now.Unix()).Scan(&result.Certificate, &notAfter)
		if errors.Is(err, sql.ErrNoRows) {
			rejected = ErrCredentials
			return audit(ctx, tx, now, agentID, "agent.recover", agentID, hostID, "denied", "credential_replaced_or_expired")
		}
		if err != nil {
			return err
		}
		result.AgentID, result.HostID, result.NotAfter = agentID, hostID, notAfter
		return audit(ctx, tx, now, agentID, "agent.recover", agentID, hostID, "success", "")
	})
	if err != nil {
		return AgentCredential{}, err
	}
	if rejected != nil {
		return AgentCredential{}, rejected
	}
	return result, nil
}

// AgentPrincipal is an authenticated Agent for one request.
type AgentPrincipal struct {
	AgentID string
	HostID  string
	Serial  string
}

// agentAuthenticate checks the current trust state for every Agent request,
// in the same transaction as whatever the request changes. A valid CA
// signature alone is never enough.
func agentAuthenticate(ctx context.Context, tx *sql.Tx, serial, agentID string, now time.Time) (AgentPrincipal, error) {
	var owner, agentState, hostID, hostState string
	var notAfter int64
	var revoked, retire sql.NullInt64
	var replaces sql.NullString
	err := tx.QueryRow("SELECT c.agent_id,c.not_after,c.revoked_at,c.retire_at,c.replaces,a.state,a.host_id,h.state FROM agent_credentials c JOIN agents a ON a.id=c.agent_id JOIN hosts h ON h.id=a.host_id WHERE c.serial=?", serial).Scan(&owner, &notAfter, &revoked, &retire, &replaces, &agentState, &hostID, &hostState)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentPrincipal{}, ErrCredentials
	}
	if err != nil {
		return AgentPrincipal{}, err
	}
	if owner != agentID || revoked.Valid || now.Unix() >= notAfter || (retire.Valid && now.Unix() >= retire.Int64) || agentState != "active" || hostState != "active" {
		return AgentPrincipal{}, ErrCredentials
	}
	if replaces.Valid {
		// The successor is in use, so the replaced credential retires now.
		result, err := tx.Exec("UPDATE agent_credentials SET revoked_at=? WHERE serial=? AND revoked_at IS NULL", now.Unix(), replaces.String)
		if err != nil {
			return AgentPrincipal{}, err
		}
		if count, err := result.RowsAffected(); err != nil {
			return AgentPrincipal{}, err
		} else if count > 0 {
			if err := auditWithDetails(ctx, tx, now, agentID, "agent.credential.retire", agentID, hostID, "success", "successor_in_use", map[string]any{"serial": replaces.String, "successor": serial}); err != nil {
				return AgentPrincipal{}, err
			}
		}
	}
	return AgentPrincipal{AgentID: agentID, HostID: hostID, Serial: serial}, nil
}

// AuthenticateAgent is for read-only Agent requests that change nothing else.
func (s *Store) AuthenticateAgent(ctx context.Context, serial, agentID string, now time.Time) (AgentPrincipal, error) {
	var p AgentPrincipal
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = agentAuthenticate(ctx, tx, serial, agentID, now)
		return err
	})
	return p, err
}

type HeartbeatResult struct {
	RenewAfter     int64 `json:"renew_after"`
	SampleInterval int64 `json:"sample_interval"`
}

func (s *Store) Heartbeat(ctx context.Context, serial, agentID, version string, capabilities []string, now time.Time) (HeartbeatResult, error) {
	encoded, err := json.Marshal(capabilities)
	if err != nil {
		return HeartbeatResult{}, err
	}
	var result HeartbeatResult
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		p, err := agentAuthenticate(ctx, tx, serial, agentID, now)
		if err != nil {
			return err
		}
		var notAfter int64
		if err := tx.QueryRow("SELECT not_after FROM agent_credentials WHERE serial=?", p.Serial).Scan(&notAfter); err != nil {
			return err
		}
		result.RenewAfter = notAfter - int64(renewalWindow/time.Second)
		if err := tx.QueryRow("SELECT sample_interval_seconds FROM monitoring_settings WHERE singleton=1").Scan(&result.SampleInterval); err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE agents SET last_seen=?,version=?,capabilities=? WHERE id=?", now.Unix(), version, string(encoded), p.AgentID)
		return err
	})
	return result, err
}

// RenewCredential issues a successor for a valid credential and a new key.
// Repeating the same request returns the same successor, so a lost response
// can be retried without minting more certificates.
func (s *Store) RenewCredential(ctx context.Context, serial, agentID string, request []byte, issuer Issuer, now time.Time) (AgentCredential, error) {
	key, err := agentca.ParseRequest(request)
	if err != nil {
		return AgentCredential{}, ErrInput
	}
	keyHash, err := agentca.KeyHash(key)
	if err != nil {
		return AgentCredential{}, err
	}
	var result AgentCredential
	var rejected error
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		p, err := agentAuthenticate(ctx, tx, serial, agentID, now)
		if err != nil {
			return err
		}
		var current []byte
		var notAfter int64
		if err := tx.QueryRow("SELECT public_key_hash,not_after FROM agent_credentials WHERE serial=?", serial).Scan(&current, &notAfter); err != nil {
			return err
		}
		var successorKey []byte
		var successorPEM string
		var successorNotAfter int64
		var successorRevoked sql.NullInt64
		err = tx.QueryRow("SELECT public_key_hash,cert_pem,not_after,revoked_at FROM agent_credentials WHERE replaces=?", serial).Scan(&successorKey, &successorPEM, &successorNotAfter, &successorRevoked)
		if err == nil {
			if string(successorKey) != string(keyHash) || successorRevoked.Valid {
				rejected = ErrConflict
				return audit(ctx, tx, now, p.AgentID, "agent.credential.renew", p.AgentID, p.HostID, "denied", "already_renewed")
			}
			result = AgentCredential{AgentID: p.AgentID, HostID: p.HostID, Certificate: successorPEM, NotAfter: successorNotAfter}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if string(current) == string(keyHash) {
			rejected = ErrInput
			return audit(ctx, tx, now, p.AgentID, "agent.credential.renew", p.AgentID, p.HostID, "denied", "key_reused")
		}
		credential, err := issuer.Issue(key, p.AgentID, now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO agent_credentials(serial,agent_id,public_key_hash,cert_pem,issued_at,not_after,replaces) VALUES(?,?,?,?,?,?,?)", credential.Serial, p.AgentID, credential.PublicKeyHash, credential.CertPEM, now.Unix(), credential.NotAfter.Unix(), serial); err != nil {
			return err
		}
		retire := min(notAfter, now.Add(credentialGrace).Unix())
		if _, err := tx.Exec("UPDATE agent_credentials SET retire_at=? WHERE serial=?", retire, serial); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE agents SET public_key_hash=? WHERE id=?", credential.PublicKeyHash, p.AgentID); err != nil {
			return err
		}
		result = AgentCredential{AgentID: p.AgentID, HostID: p.HostID, Certificate: credential.CertPEM, NotAfter: credential.NotAfter.Unix()}
		return auditWithDetails(ctx, tx, now, p.AgentID, "agent.credential.renew", p.AgentID, p.HostID, "success", "", map[string]any{"serial": credential.Serial, "replaces": serial, "retire_at": retire, "not_after": result.NotAfter})
	})
	if err != nil {
		return AgentCredential{}, err
	}
	if rejected != nil {
		return AgentCredential{}, rejected
	}
	return result, nil
}

// endHostTrust revokes everything an Agent could use for a host. It runs
// inside the caller's transaction, before the change is reported.
func endHostTrust(tx *sql.Tx, hostID, agentState, reason string, now time.Time) error {
	for _, q := range []struct {
		query string
		args  []any
	}{
		{"UPDATE agent_credentials SET revoked_at=coalesce(revoked_at,?) WHERE agent_id IN (SELECT id FROM agents WHERE host_id=?)", []any{now.Unix(), hostID}},
		{"UPDATE agents SET state=? WHERE host_id=? AND state='active'", []any{agentState, hostID}},
		{"UPDATE enrollments SET revoked_at=? WHERE host_id=? AND consumed_at IS NULL AND revoked_at IS NULL", []any{now.Unix(), hostID}},
		{"UPDATE tasks SET state='cancelled',reason=?,updated_at=?,finished_at=? WHERE host_id=? AND state='queued'", []any{reason, now.Unix(), now.Unix(), hostID}},
	} {
		if _, err := tx.Exec(q.query, q.args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) changeHostTrust(ctx context.Context, token, csrf, hostID, action, to string, now time.Time) error {
	var rejected error
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, hash, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !security.EqualDigest(csrf, hash) {
			rejected = ErrDenied
			return audit(ctx, tx, now, p.ID, action, "", "", "denied", "invalid_csrf")
		}
		state, err := hostState(tx, p, hostID)
		if err != nil {
			return err
		}
		if state == "" {
			rejected = ErrNotFound
			return audit(ctx, tx, now, p.ID, action, "", "", "denied", "not_found")
		}
		if !p.Allows(rbac.HostManage, hostID) {
			rejected = ErrDenied
			return audit(ctx, tx, now, p.ID, action, hostID, hostID, "denied", "forbidden")
		}
		if state == to || (to == "revoked" && state != "pending" && state != "active") {
			rejected = ErrConflict
			return audit(ctx, tx, now, p.ID, action, hostID, hostID, "denied", "host_"+state)
		}
		if err := endHostTrust(tx, hostID, to, "host_"+to, now); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE hosts SET state=? WHERE id=?", to, hostID); err != nil {
			return err
		}
		return auditWithDetails(ctx, tx, now, p.ID, action, hostID, hostID, "success", "", map[string]any{"before": map[string]any{"state": state}, "after": map[string]any{"state": to}})
	})
	if err != nil {
		return err
	}
	return rejected
}

// RevokeHost ends the Agent's trust but keeps the host and its history.
func (s *Store) RevokeHost(ctx context.Context, token, csrf, hostID string, now time.Time) error {
	return s.changeHostTrust(ctx, token, csrf, hostID, "host.revoke", "revoked", now)
}

// DeleteHost ends the Agent's trust and hides the host. Audit records stay.
func (s *Store) DeleteHost(ctx context.Context, token, csrf, hostID string, now time.Time) error {
	return s.changeHostTrust(ctx, token, csrf, hostID, "host.delete", "deleted", now)
}

// restoreAgentTrust runs on a restored database: the old CA is gone, so no
// restored credential may work. Hosts go back to pending to enroll again.
func restoreAgentTrust(tx *sql.Tx, now time.Time) error {
	for _, q := range []string{
		"UPDATE agent_credentials SET revoked_at=coalesce(revoked_at,?)",
		"UPDATE agents SET state='revoked' WHERE state='active'",
		"UPDATE enrollments SET revoked_at=coalesce(revoked_at,?) WHERE consumed_at IS NULL",
		"UPDATE hosts SET state='pending' WHERE state='active'",
	} {
		var err error
		if strings.Contains(q, "?") {
			_, err = tx.Exec(q, now.Unix())
		} else {
			_, err = tx.Exec(q)
		}
		if err != nil {
			return err
		}
	}
	_, err := tx.Exec("DELETE FROM enrollment_challenges")
	return err
}

func agentInfo(tx *sql.Tx, hostID string, now time.Time) (*AgentInfo, error) {
	var info AgentInfo
	var lastSeen sql.NullInt64
	var capabilities string
	err := tx.QueryRow(`SELECT a.id,a.version,a.capabilities,a.last_seen,coalesce((SELECT max(not_after) FROM agent_credentials c WHERE c.agent_id=a.id AND c.revoked_at IS NULL),0)
		FROM agents a WHERE a.host_id=? AND a.state='active'`, hostID).Scan(&info.ID, &info.Version, &capabilities, &lastSeen, &info.CredentialExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(capabilities), &info.Capabilities); err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		info.LastSeen = &lastSeen.Int64
		info.Connected = now.Unix()-lastSeen.Int64 < int64(disconnectedAfter/time.Second)
	}
	info.CredentialRenewalDue = info.CredentialExpiresAt-now.Unix() < int64(renewalWindow/time.Second)
	return &info, nil
}
