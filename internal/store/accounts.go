package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Enabled  bool   `json:"enabled"`
}

type Invitation struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	Username  string `json:"username"`
	ExpiresAt int64  `json:"expires_at"`
}

// Domain rejections are audited and committed. Callbacks must do all domain
// validation before changing state; database errors always roll back.
func (s *Store) accountMutation(ctx context.Context, token, csrf, action, target string, now time.Time, fn func(*sql.Tx, rbac.Principal) error) error {
	var rejected error
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, hash, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if target == "" {
			target = p.ID
		}
		if !security.EqualDigest(csrf, hash) {
			rejected = ErrDenied
		} else {
			rejected = fn(tx, p)
		}
		if rejected != nil {
			reason := ""
			switch {
			case errors.Is(rejected, ErrDenied):
				reason = "forbidden"
			case errors.Is(rejected, ErrCredentials):
				reason = "invalid_credentials"
			case errors.Is(rejected, ErrConflict):
				reason = "conflict"
			case errors.Is(rejected, ErrNotFound):
				reason = "not_found"
			case errors.Is(rejected, ErrInput):
				reason = "invalid_input"
			default:
				return rejected
			}
			return audit(tx, now, p.ID, action, target, "", "denied", reason)
		}
		return audit(tx, now, p.ID, action, target, "", "success", "")
	})
	if err != nil {
		return err
	}
	return rejected
}

func (s *Store) Reauthenticate(ctx context.Context, token, csrf, password string, now time.Time) error {
	return s.accountMutation(ctx, token, csrf, "auth.reauthenticate", "", now, func(tx *sql.Tx, p rbac.Principal) error {
		var hash string
		if err := tx.QueryRow("SELECT password_hash FROM users WHERE id=?", p.ID).Scan(&hash); err != nil {
			return err
		}
		if !security.VerifyPassword(password, hash) {
			return ErrCredentials
		}
		_, err := tx.Exec("UPDATE sessions SET reauthenticated_at=? WHERE token_hash=?", now.Unix(), security.Digest(token))
		return err
	})
}

func (s *Store) ChangePassword(ctx context.Context, token, csrf, currentPassword, newPassword string, now time.Time) error {
	if len(newPassword) < 12 || len(newPassword) > 128 {
		return ErrInput
	}
	newHash, err := security.HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.accountMutation(ctx, token, csrf, "auth.password.change", "", now, func(tx *sql.Tx, p rbac.Principal) error {
		var hash string
		if err := tx.QueryRow("SELECT password_hash FROM users WHERE id=?", p.ID).Scan(&hash); err != nil {
			return err
		}
		if !security.VerifyPassword(currentPassword, hash) {
			return ErrCredentials
		}
		if _, err := tx.Exec("UPDATE users SET password_hash=? WHERE id=?", newHash, p.ID); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM sessions WHERE user_id=?", p.ID)
		return err
	})
}

func (s *Store) Users(ctx context.Context, token string, now time.Time) ([]User, error) {
	users := []User{}
	var denied bool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !p.Allows(rbac.IdentityManage, "") {
			denied = true
			return audit(tx, now, p.ID, "user.list", "", "", "denied", "forbidden")
		}
		rows, err := tx.Query("SELECT id,username,enabled FROM users ORDER BY username")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var u User
			if err := rows.Scan(&u.ID, &u.Username, &u.Enabled); err != nil {
				return err
			}
			users = append(users, u)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if denied {
		return nil, ErrDenied
	}
	return users, nil
}

func owner(p rbac.Principal) bool {
	for _, g := range p.Grants {
		if g.Role == rbac.Owner && g.All {
			return true
		}
	}
	return false
}

func (s *Store) SetUserEnabled(ctx context.Context, token, csrf, targetID string, enabled bool, now time.Time) error {
	action := "user.disable"
	if enabled {
		action = "user.enable"
	}
	return s.accountMutation(ctx, token, csrf, action, targetID, now, func(tx *sql.Tx, p rbac.Principal) error {
		if !p.Allows(rbac.IdentityManage, "") {
			return ErrDenied
		}
		target, err := principal(tx, targetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if owner(target) {
			if !p.Allows(rbac.OwnerManage, "") {
				return ErrDenied
			}
			var recent sql.NullInt64
			if err := tx.QueryRow("SELECT reauthenticated_at FROM sessions WHERE token_hash=?", security.Digest(token)).Scan(&recent); err != nil {
				return err
			}
			if !recent.Valid || now.Unix() < recent.Int64 || now.Unix()-recent.Int64 >= 300 {
				return ErrDenied
			}
			if target.Enabled && !enabled {
				var count int
				if err := tx.QueryRow(`SELECT count(DISTINCT u.id) FROM users u JOIN grants g ON g.user_id=u.id WHERE u.enabled=1 AND g.role='Owner' AND g.all_hosts=1`).Scan(&count); err != nil {
					return err
				}
				if count <= 1 {
					return ErrConflict
				}
			}
		}
		if _, err := tx.Exec("UPDATE users SET enabled=? WHERE id=?", enabled, targetID); err != nil {
			return err
		}
		if !enabled {
			if _, err := tx.Exec("DELETE FROM sessions WHERE user_id=?", targetID); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE invitations SET revoked_at=? WHERE issuer_id=? AND consumed_at IS NULL AND revoked_at IS NULL", now.Unix(), targetID); err != nil {
				return err
			}
		}
		return nil
	})
}

func validInvitationGrant(tx *sql.Tx, p rbac.Principal, g rbac.Grant) (bool, error) {
	if !p.CanDelegate(g) {
		return false, nil
	}
	if !g.All {
		var state string
		err := tx.QueryRow("SELECT state FROM hosts WHERE id=?", g.HostID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if state != "pending" && state != "active" {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) CreateInvitation(ctx context.Context, token, csrf, username string, grants []rbac.Grant, now time.Time) (Invitation, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if !validUsername(username) || len(grants) < 1 || len(grants) > 32 {
		return Invitation{}, ErrInput
	}
	result := Invitation{ID: security.Token(), Token: security.Token(), Username: username, ExpiresAt: now.Add(24 * time.Hour).Unix()}
	err := s.accountMutation(ctx, token, csrf, "invitation.create", result.ID, now, func(tx *sql.Tx, p rbac.Principal) error {
		if !p.Allows(rbac.IdentityManage, "") {
			return ErrDenied
		}
		for _, g := range grants {
			valid, err := validInvitationGrant(tx, p, g)
			if err != nil {
				return err
			}
			if !valid {
				return ErrDenied
			}
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM users WHERE username=?", username).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrConflict
		}
		if _, err := tx.Exec("UPDATE invitations SET revoked_at=? WHERE username=? AND consumed_at IS NULL AND revoked_at IS NULL", now.Unix(), username); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO invitations(id,token_hash,username,issuer_id,created_at,expires_at) VALUES(?,?,?,?,?,?)", result.ID, security.Digest(result.Token), username, p.ID, now.Unix(), result.ExpiresAt); err != nil {
			return err
		}
		for _, g := range grants {
			var host any
			if !g.All {
				host = g.HostID
			}
			if _, err := tx.Exec("INSERT INTO invitation_grants VALUES(?,?,?,?)", result.ID, g.Role, g.All, host); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Invitation{}, err
	}
	return result, nil
}

func (s *Store) RevokeInvitation(ctx context.Context, token, csrf, id string, now time.Time) error {
	return s.accountMutation(ctx, token, csrf, "invitation.revoke", id, now, func(tx *sql.Tx, p rbac.Principal) error {
		if !p.Allows(rbac.IdentityManage, "") {
			return ErrDenied
		}
		var consumed sql.NullInt64
		err := tx.QueryRow("SELECT consumed_at FROM invitations WHERE id=?", id).Scan(&consumed)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if consumed.Valid {
			return ErrConflict
		}
		_, err = tx.Exec("UPDATE invitations SET revoked_at=coalesce(revoked_at,?) WHERE id=?", now.Unix(), id)
		return err
	})
}

func (s *Store) AcceptInvitation(ctx context.Context, token, password string, now time.Time) (User, error) {
	if len(password) < 12 || len(password) > 128 {
		return User{}, ErrInput
	}
	hash, err := security.HashPassword(password)
	if err != nil {
		return User{}, err
	}
	var result User
	var rejected bool
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var id, username, issuerID string
		var expiry, created int64
		var revoked, consumed sql.NullInt64
		err := tx.QueryRow("SELECT id,username,issuer_id,created_at,expires_at,revoked_at,consumed_at FROM invitations WHERE token_hash=?", security.Digest(token)).Scan(&id, &username, &issuerID, &created, &expiry, &revoked, &consumed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		reject := func() error {
			rejected = true
			return audit(tx, now, "anonymous", "invitation.accept", id, "", "denied", "invalid_invitation")
		}
		if errors.Is(err, sql.ErrNoRows) || revoked.Valid || consumed.Valid {
			return reject()
		}
		invalidate := func() error {
			if _, err := tx.Exec("UPDATE invitations SET revoked_at=? WHERE id=?", now.Unix(), id); err != nil {
				return err
			}
			return reject()
		}
		if now.Unix() < created || now.Unix() >= expiry {
			return invalidate()
		}
		issuer, err := principal(tx, issuerID)
		if err != nil {
			return err
		}
		if !issuer.Allows(rbac.IdentityManage, "") {
			return invalidate()
		}
		rows, err := tx.Query("SELECT role,all_hosts,coalesce(host_id,'') FROM invitation_grants WHERE invitation_id=?", id)
		if err != nil {
			return err
		}
		grants := []rbac.Grant{}
		for rows.Next() {
			var g rbac.Grant
			if err := rows.Scan(&g.Role, &g.All, &g.HostID); err != nil {
				rows.Close()
				return err
			}
			grants = append(grants, g)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(grants) == 0 {
			return invalidate()
		}
		for _, g := range grants {
			valid, err := validInvitationGrant(tx, issuer, g)
			if err != nil {
				return err
			}
			if !valid {
				return invalidate()
			}
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM users WHERE username=?", username).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return invalidate()
		}
		result = User{ID: security.Token(), Username: username, Enabled: true}
		if _, err := tx.Exec("INSERT INTO users(id,username,password_hash) VALUES(?,?,?)", result.ID, username, hash); err != nil {
			return err
		}
		for _, g := range grants {
			var host any
			if !g.All {
				host = g.HostID
			}
			if _, err := tx.Exec("INSERT INTO grants(id,user_id,role,all_hosts,host_id) VALUES(?,?,?,?,?)", security.Token(), result.ID, g.Role, g.All, host); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("UPDATE invitations SET consumed_at=? WHERE id=?", now.Unix(), id); err != nil {
			return err
		}
		return audit(tx, now, result.ID, "invitation.accept", id, "", "success", "")
	})
	if err != nil {
		return User{}, err
	}
	if rejected {
		return User{}, ErrCredentials
	}
	return result, nil
}
