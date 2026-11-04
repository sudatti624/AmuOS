package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

// Snapshots are projections of named fields, never whole database rows.
func userAuditState(tx *sql.Tx, id string) (any, error) {
	var username string
	var enabled bool
	var version, sessions, invitations int64
	err := tx.QueryRow("SELECT username,enabled,authorization_version FROM users WHERE id=?", id).Scan(&username, &enabled, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	grants, err := grantsFor(tx, "user_id", id)
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRow("SELECT count(*) FROM sessions WHERE user_id=?", id).Scan(&sessions); err != nil {
		return nil, err
	}
	if err = tx.QueryRow("SELECT count(*) FROM invitations WHERE issuer_id=? AND consumed_at IS NULL AND revoked_at IS NULL", id).Scan(&invitations); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "username": username, "enabled": enabled, "authorization_version": version, "direct_grants": grants, "session_count": sessions, "open_invitation_count": invitations}, nil
}

func auditSnapshot(tx *sql.Tx, p rbac.Principal, token, action, target string) (any, error) {
	if strings.HasPrefix(action, "auth.") {
		state, err := userAuditState(tx, p.ID)
		if err != nil {
			return nil, err
		}
		var reauthenticated sql.NullInt64
		err = tx.QueryRow("SELECT reauthenticated_at FROM sessions WHERE token_hash=?", security.Digest(token)).Scan(&reauthenticated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return map[string]any{"user": state, "session_reauthenticated": reauthenticated.Valid}, nil
	}
	if !p.Allows(rbac.IdentityManage, "") {
		return nil, nil
	}
	switch {
	case action == "owner.transfer":
		source, err := userAuditState(tx, p.ID)
		if err != nil {
			return nil, err
		}
		dest, err := userAuditState(tx, target)
		return map[string]any{"source": source, "destination": dest}, err
	case strings.HasPrefix(action, "user.") || action == "owner.add" || action == "owner.remove":
		return userAuditState(tx, target)
	case strings.HasPrefix(action, "group."):
		group, err := findGroup(tx, target)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return group, err
	case strings.HasPrefix(action, "invitation."):
		var username, issuer string
		var expiry int64
		var revoked, consumed sql.NullInt64
		err := tx.QueryRow("SELECT username,issuer_id,expires_at,revoked_at,consumed_at FROM invitations WHERE id=?", target).Scan(&username, &issuer, &expiry, &revoked, &consumed)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		rows, err := tx.Query("SELECT role,all_hosts,coalesce(host_id,'') FROM invitation_grants WHERE invitation_id=?", target)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		grants := []rbac.Grant{}
		for rows.Next() {
			var g rbac.Grant
			if err := rows.Scan(&g.Role, &g.All, &g.HostID); err != nil {
				return nil, err
			}
			grants = append(grants, g)
		}
		return map[string]any{"id": target, "username": username, "issuer_id": issuer, "expires_at": expiry, "revoked": revoked.Valid, "consumed": consumed.Valid, "grants": grants}, rows.Err()
	}
	return nil, nil
}
