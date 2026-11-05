package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
	_ "modernc.org/sqlite"
)

var (
	ErrDenied      = errors.New("forbidden")
	ErrCredentials = errors.New("invalid credentials")
	ErrConflict    = errors.New("conflict")
	ErrInput       = errors.New("invalid input")
	ErrNotFound    = errors.New("not found")
)

//go:embed migrations/*.sql
var migrations embed.FS

var migrationNames = []string{"001_foundation.sql", "002_accounts.sql", "003_authorization.sql", "004_audit_requests.sql", "005_audit_retention.sql", "006_audit_maintenance.sql", "007_idempotency.sql", "008_local_maintenance.sql", "009_tasks.sql", "010_agents.sql", "011_monitoring.sql"}

type Store struct{ db *sql.DB }
type Host struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	State    string     `json:"state"`
	Agent    *AgentInfo `json:"agent"`
	Replayed bool       `json:"-"`
}
type Event struct {
	ID        int64           `json:"id"`
	At        int64           `json:"at"`
	Actor     string          `json:"actor_id"`
	Action    string          `json:"action"`
	Target    string          `json:"target_id"`
	HostID    string          `json:"host_id,omitempty"`
	Outcome   string          `json:"outcome"`
	Reason    string          `json:"reason"`
	RequestID string          `json:"request_id,omitempty"`
	Details   json.RawMessage `json:"details"`
}
type Session struct {
	Token  string
	CSRF   string
	UserID string
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	fail := func(err error) (*Store, error) { _ = db.Close(); return nil, err }
	for _, pragma := range []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL"} {
		if _, err := db.Exec(pragma); err != nil {
			return fail(err)
		}
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version < 0 {
		return fail(errors.New("invalid database schema version"))
	}
	if version > len(migrationNames) {
		return fail(errors.New("database schema is newer than this binary"))
	}
	if version < len(migrationNames) {
		tx, err := db.Begin()
		if err != nil {
			return fail(err)
		}
		for i := version; i < len(migrationNames); i++ {
			contents, readErr := migrations.ReadFile("migrations/" + migrationNames[i])
			if readErr != nil {
				err = readErr
				break
			}
			if _, err = tx.Exec(string(contents)); err != nil {
				break
			}
			if _, err = tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
				break
			}
		}
		if err != nil {
			_ = tx.Rollback()
			return fail(err)
		}
		if err = tx.Commit(); err != nil {
			return fail(err)
		}
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func audit(ctx context.Context, tx *sql.Tx, now time.Time, actor, action, target, host, outcome, reason string) error {
	return auditWithDetails(ctx, tx, now, actor, action, target, host, outcome, reason, nil)
}

func auditWithDetails(ctx context.Context, tx *sql.Tx, now time.Time, actor, action, target, host, outcome, reason string, details map[string]any) error {
	var hostValue any
	if host != "" {
		hostValue = host
	}
	if details == nil {
		details = map[string]any{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	if len(encoded) > 64*1024 {
		return errors.New("audit details exceed limit")
	}
	_, err = tx.Exec(`INSERT INTO audit_events(at,actor_id,action,target_id,host_id,outcome,reason,request_id,details) VALUES(?,?,?,?,?,?,?,?,?)`, now.Unix(), actor, action, target, hostValue, outcome, reason, RequestID(ctx), string(encoded))
	return err
}

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// PrepareBootstrap is local-only. Rotation invalidates any earlier setup token.
func (s *Store) PrepareBootstrap(ctx context.Context, now time.Time) (string, error) {
	token := security.Token()
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrConflict
		}
		_, err := tx.Exec("INSERT INTO bootstrap VALUES(1,?,?) ON CONFLICT(singleton) DO UPDATE SET token_hash=excluded.token_hash,expires_at=excluded.expires_at", security.Digest(token), now.Add(10*time.Minute).Unix())
		if err != nil {
			return err
		}
		return audit(ctx, tx, now, "local", "setup.prepare", "", "", "success", "")
	})
	return token, err
}

func validUsername(name string) bool {
	if len(name) < 3 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func (s *Store) Setup(ctx context.Context, token, username, password string, now time.Time) error {
	username = strings.ToLower(strings.TrimSpace(username))
	if !validUsername(username) {
		return fmt.Errorf("%w: username", ErrInput)
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var stored []byte
		var expiry int64
		err := tx.QueryRow("SELECT token_hash,expires_at FROM bootstrap WHERE singleton=1").Scan(&stored, &expiry)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err != nil || now.Unix() >= expiry || !security.EqualDigest(token, stored) {
			return ErrCredentials
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrConflict
		}
		id := security.Token()
		if _, err := tx.Exec("INSERT INTO users(id,username,password_hash) VALUES(?,?,?)", id, username, hash); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO grants(id,user_id,role,all_hosts) VALUES(?,?,'Owner',1)", security.Token(), id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM bootstrap"); err != nil {
			return err
		}
		state, err := userAuditState(tx, id)
		if err != nil {
			return err
		}
		return auditWithDetails(ctx, tx, now, id, "setup.complete", id, "", "success", "", map[string]any{"before": nil, "after": state})
	})
}

func principal(tx *sql.Tx, id string) (rbac.Principal, error) {
	p := rbac.Principal{ID: id}
	if err := tx.QueryRow("SELECT enabled,authorization_version FROM users WHERE id=?", id).Scan(&p.Enabled, &p.Version); err != nil {
		return p, err
	}
	rows, err := tx.Query(`SELECT role,all_hosts,coalesce(host_id,'') FROM grants WHERE user_id=? OR group_id IN (SELECT group_id FROM memberships WHERE user_id=?)`, id, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var g rbac.Grant
		if err := rows.Scan(&g.Role, &g.All, &g.HostID); err != nil {
			return p, err
		}
		p.Grants = append(p.Grants, g)
	}
	return p, rows.Err()
}

func authenticate(tx *sql.Tx, token string, now time.Time) (rbac.Principal, []byte, error) {
	var id string
	var last, expiry int64
	var csrf []byte
	err := tx.QueryRow("SELECT user_id,csrf_hash,last_seen,expires_at FROM sessions WHERE token_hash=?", security.Digest(token)).Scan(&id, &csrf, &last, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return rbac.Principal{}, nil, ErrCredentials
	}
	if err != nil {
		return rbac.Principal{}, nil, err
	}
	if now.Unix() >= expiry || now.Unix()-last >= 1800 || now.Unix() < last {
		return rbac.Principal{}, nil, ErrCredentials
	}
	p, err := principal(tx, id)
	if err != nil {
		return p, nil, err
	}
	if !p.Enabled {
		return p, nil, ErrCredentials
	}
	if _, err := tx.Exec("UPDATE sessions SET last_seen=? WHERE token_hash=?", now.Unix(), security.Digest(token)); err != nil {
		return p, nil, err
	}
	return p, csrf, nil
}

func (s *Store) Login(ctx context.Context, username, password string, now time.Time) (Session, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	result := Session{Token: security.Token(), CSRF: security.Token()}
	var rejected bool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var hash string
		var enabled bool
		err := tx.QueryRow("SELECT id,password_hash,enabled FROM users WHERE username=?", username).Scan(&result.UserID, &hash, &enabled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			// Keep unknown-user checks comparable to real password verification.
			hash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		}
		valid := security.VerifyPassword(password, hash)
		if !valid || !enabled {
			rejected = true
			return audit(ctx, tx, now, "anonymous", "auth.login", "", "", "denied", "invalid_credentials")
		}
		if _, err := tx.Exec("DELETE FROM sessions WHERE expires_at<=? OR last_seen<=?", now.Unix(), now.Add(-30*time.Minute).Unix()); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO sessions(token_hash,csrf_hash,user_id,created_at,last_seen,expires_at) VALUES(?,?,?,?,?,?)", security.Digest(result.Token), security.Digest(result.CSRF), result.UserID, now.Unix(), now.Unix(), now.Add(12*time.Hour).Unix()); err != nil {
			return err
		}
		return audit(ctx, tx, now, result.UserID, "auth.login", result.UserID, "", "success", "")
	})
	if err != nil {
		return Session{}, err
	}
	if rejected {
		return Session{}, ErrCredentials
	}
	return result, nil
}

func (s *Store) Identity(ctx context.Context, token string, now time.Time) (rbac.Principal, error) {
	var p rbac.Principal
	err := s.transaction(ctx, func(tx *sql.Tx) error { var err error; p, _, err = authenticate(tx, token, now); return err })
	return p, err
}

// Profile returns the session principal and its own username for display.
func (s *Store) Profile(ctx context.Context, token string, now time.Time) (rbac.Principal, string, error) {
	var p rbac.Principal
	var username string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		if p, _, err = authenticate(tx, token, now); err != nil {
			return err
		}
		return tx.QueryRow("SELECT username FROM users WHERE id=?", p.ID).Scan(&username)
	})
	return p, username, err
}

func (s *Store) Logout(ctx context.Context, token, csrf string, now time.Time) error {
	var denied bool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, hash, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !security.EqualDigest(csrf, hash) {
			denied = true
			return audit(ctx, tx, now, p.ID, "auth.logout", p.ID, "", "denied", "invalid_csrf")
		}
		if _, err := tx.Exec("DELETE FROM sessions WHERE token_hash=?", security.Digest(token)); err != nil {
			return err
		}
		return audit(ctx, tx, now, p.ID, "auth.logout", p.ID, "", "success", "")
	})
	if err != nil {
		return err
	}
	if denied {
		return ErrDenied
	}
	return nil
}

func (s *Store) Hosts(ctx context.Context, token string, now time.Time) ([]Host, error) {
	hosts := []Host{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		rows, err := tx.Query("SELECT id,name,state FROM hosts WHERE state!='deleted' ORDER BY id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var h Host
			if err := rows.Scan(&h.ID, &h.Name, &h.State); err != nil {
				return err
			}
			if p.Allows(rbac.HostRead, h.ID) {
				hosts = append(hosts, h)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		for i := range hosts {
			if hosts[i].Agent, err = agentInfo(tx, hosts[i].ID, now); err != nil {
				return err
			}
		}
		return nil
	})
	return hosts, err
}

func (s *Store) CreateHost(ctx context.Context, token, csrf, name string, now time.Time) (Host, error) {
	host := Host{ID: security.Token(), Name: strings.TrimSpace(name), State: "pending"}
	if len(host.Name) < 1 || len(host.Name) > 128 {
		return Host{}, ErrInput
	}
	var rejected error
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, hash, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !security.EqualDigest(csrf, hash) || !p.Allows(rbac.HostManage, "") {
			rejected = ErrDenied
			return audit(ctx, tx, now, p.ID, "host.create", "", "", "denied", "forbidden")
		}
		key := idempotencyKey(ctx)
		if key != "" {
			previous, found, reason, err := lookupHostOperation(tx, p.ID, key, host.Name, now)
			if err != nil {
				return err
			}
			if found {
				if reason != "" {
					rejected = ErrConflict
					return audit(ctx, tx, now, p.ID, "host.create", "host-registry", "", "denied", reason)
				}
				host = previous
				return auditWithDetails(ctx, tx, now, p.ID, "host.create.replay", host.ID, host.ID, "success", "", map[string]any{"replayed": true, "resource_id": host.ID})
			}
		}
		if _, err := tx.Exec("INSERT INTO hosts(id,name) VALUES(?,?)", host.ID, host.Name); err != nil {
			return err
		}
		if key != "" {
			if err := recordHostOperation(tx, p.ID, key, host, now); err != nil {
				return err
			}
		}
		return auditWithDetails(ctx, tx, now, p.ID, "host.create", host.ID, host.ID, "success", "", map[string]any{"before": nil, "after": host})
	})
	if err != nil {
		return Host{}, err
	}
	if rejected != nil {
		return Host{}, rejected
	}
	return host, nil
}

func (s *Store) Events(ctx context.Context, token string, now time.Time) ([]Event, error) {
	events := []Event{}
	var denied bool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		allowed := p.Allows(rbac.AuditRead, "")
		if !allowed {
			for _, g := range p.Grants {
				if p.Allows(rbac.AuditRead, g.HostID) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			denied = true
			return audit(ctx, tx, now, p.ID, "audit.read", "", "", "denied", "forbidden")
		}
		rows, err := tx.Query("SELECT id,at,actor_id,action,target_id,coalesce(host_id,''),outcome,reason,request_id,details FROM audit_events ORDER BY id DESC")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e Event
			var details string
			if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &e.HostID, &e.Outcome, &e.Reason, &e.RequestID, &details); err != nil {
				return err
			}
			e.Details = json.RawMessage(details)
			if p.Allows(rbac.AuditRead, e.HostID) {
				if !json.Valid(e.Details) {
					return errors.New("invalid stored audit details")
				}
				events = append(events, e)
				if len(events) == 100 {
					break
				}
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if denied {
		return nil, ErrDenied
	}
	return events, nil
}
