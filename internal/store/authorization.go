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

type Group struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Members []string     `json:"user_ids"`
	Grants  []rbac.Grant `json:"grants"`
}

func requireRecentOwner(tx *sql.Tx, p rbac.Principal, token string, now time.Time) error {
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
	return nil
}

func bumpAuthorization(tx *sql.Tx, ids []string, now time.Time) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err := tx.Exec("UPDATE users SET authorization_version=authorization_version+1 WHERE id=?", id); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE sessions SET reauthenticated_at=NULL WHERE user_id=?", id); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE invitations SET revoked_at=? WHERE issuer_id=? AND consumed_at IS NULL AND revoked_at IS NULL", now.Unix(), id); err != nil {
			return err
		}
	}
	return nil
}

// The column is selected only by the caller, never from request input.
func grantsFor(tx *sql.Tx, column, id string) ([]rbac.Grant, error) {
	if column != "user_id" && column != "group_id" {
		return nil, ErrInput
	}
	rows, err := tx.Query("SELECT role,all_hosts,coalesce(host_id,'') FROM grants WHERE "+column+"=? ORDER BY role,all_hosts,host_id", id)
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
	return grants, rows.Err()
}

func membersFor(tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.Query("SELECT user_id FROM memberships WHERE group_id=? ORDER BY user_id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		ids = append(ids, userID)
	}
	return ids, rows.Err()
}

func findGroup(tx *sql.Tx, id string) (Group, error) {
	g := Group{ID: id}
	err := tx.QueryRow("SELECT name FROM groups WHERE id=?", id).Scan(&g.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	g.Members, err = membersFor(tx, id)
	if err != nil {
		return g, err
	}
	g.Grants, err = grantsFor(tx, "group_id", id)
	return g, err
}

func protectOwnerMembers(tx *sql.Tx, p rbac.Principal, token string, ids []string, now time.Time) error {
	for _, id := range ids {
		member, err := principal(tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if owner(member) {
			if err := requireRecentOwner(tx, p, token, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateGrants(tx *sql.Tx, p rbac.Principal, grants []rbac.Grant) error {
	if len(grants) > 32 {
		return ErrInput
	}
	seen := map[rbac.Grant]bool{}
	for _, g := range grants {
		if seen[g] {
			return ErrInput
		}
		seen[g] = true
		valid, err := validInvitationGrant(tx, p, g)
		if err != nil {
			return err
		}
		if !valid {
			return ErrDenied
		}
	}
	return nil
}

func replaceGrants(tx *sql.Tx, column, id string, grants []rbac.Grant) error {
	if column != "user_id" && column != "group_id" {
		return ErrInput
	}
	if _, err := tx.Exec("DELETE FROM grants WHERE "+column+"=?", id); err != nil {
		return err
	}
	for _, g := range grants {
		var host any
		if !g.All {
			host = g.HostID
		}
		if _, err := tx.Exec("INSERT INTO grants(id,"+column+",role,all_hosts,host_id) VALUES(?,?,?,?,?)", security.Token(), id, g.Role, g.All, host); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Groups(ctx context.Context, token string, now time.Time) ([]Group, error) {
	groups := []Group{}
	var denied bool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !p.Allows(rbac.IdentityManage, "") {
			denied = true
			return audit(ctx, tx, now, p.ID, "group.list", "", "", "denied", "forbidden")
		}
		rows, err := tx.Query("SELECT id FROM groups ORDER BY name")
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			g, err := findGroup(tx, id)
			if err != nil {
				return err
			}
			groups = append(groups, g)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if denied {
		return nil, ErrDenied
	}
	return groups, nil
}

func (s *Store) CreateGroup(ctx context.Context, token, csrf, name string, now time.Time) (Group, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !validUsername(name) {
		return Group{}, ErrInput
	}
	g := Group{ID: security.Token(), Name: name, Members: []string{}, Grants: []rbac.Grant{}}
	err := s.accountMutation(ctx, token, csrf, "group.create", g.ID, now, func(tx *sql.Tx, p rbac.Principal) error {
		if !p.Allows(rbac.IdentityManage, "") {
			return ErrDenied
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM groups WHERE name=?", name).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrConflict
		}
		_, err := tx.Exec("INSERT INTO groups(id,name) VALUES(?,?)", g.ID, g.Name)
		return err
	})
	if err != nil {
		return Group{}, err
	}
	return g, nil
}

func (s *Store) SetGroupMembers(ctx context.Context, token, csrf, id string, userIDs []string, now time.Time) error {
	if userIDs == nil || len(userIDs) > 256 {
		return ErrInput
	}
	seen := map[string]bool{}
	for _, userID := range userIDs {
		if userID == "" || seen[userID] {
			return ErrInput
		}
		seen[userID] = true
	}
	return s.accountMutation(ctx, token, csrf, "group.members.replace", id, now, func(tx *sql.Tx, p rbac.Principal) error {
		if !p.Allows(rbac.IdentityManage, "") {
			return ErrDenied
		}
		g, err := findGroup(tx, id)
		if err != nil {
			return err
		}
		ids := append(append([]string{}, g.Members...), userIDs...)
		if err := protectOwnerMembers(tx, p, token, ids, now); err != nil {
			return err
		}
		// Removals must remain possible even if an old grant references a retired host.
		existing := map[string]bool{}
		for _, memberID := range g.Members {
			existing[memberID] = true
		}
		for _, memberID := range userIDs {
			if !existing[memberID] {
				if err := validateGrants(tx, p, g.Grants); err != nil {
					return err
				}
				break
			}
		}
		if _, err := tx.Exec("DELETE FROM memberships WHERE group_id=?", id); err != nil {
			return err
		}
		for _, userID := range userIDs {
			if _, err := tx.Exec("INSERT INTO memberships(user_id,group_id) VALUES(?,?)", userID, id); err != nil {
				return err
			}
		}
		return bumpAuthorization(tx, ids, now)
	})
}

func (s *Store) SetGroupGrants(ctx context.Context, token, csrf, id string, grants []rbac.Grant, now time.Time) error {
	if grants == nil {
		return ErrInput
	}
	return s.accountMutation(ctx, token, csrf, "group.grants.replace", id, now, func(tx *sql.Tx, p rbac.Principal) error {
		if !p.Allows(rbac.IdentityManage, "") {
			return ErrDenied
		}
		g, err := findGroup(tx, id)
		if err != nil {
			return err
		}
		if err := protectOwnerMembers(tx, p, token, g.Members, now); err != nil {
			return err
		}
		if err := validateGrants(tx, p, grants); err != nil {
			return err
		}
		if err := replaceGrants(tx, "group_id", id, grants); err != nil {
			return err
		}
		return bumpAuthorization(tx, g.Members, now)
	})
}

func (s *Store) DeleteGroup(ctx context.Context, token, csrf, id string, now time.Time) error {
	return s.accountMutation(ctx, token, csrf, "group.delete", id, now, func(tx *sql.Tx, p rbac.Principal) error {
		if !p.Allows(rbac.IdentityManage, "") {
			return ErrDenied
		}
		g, err := findGroup(tx, id)
		if err != nil {
			return err
		}
		if err := protectOwnerMembers(tx, p, token, g.Members, now); err != nil {
			return err
		}
		if err := bumpAuthorization(tx, g.Members, now); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM memberships WHERE group_id=?", id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM grants WHERE group_id=?", id); err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM groups WHERE id=?", id)
		return err
	})
}

func (s *Store) UserGrants(ctx context.Context, token, id string, now time.Time) ([]rbac.Grant, error) {
	var grants []rbac.Grant
	var denied bool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !p.Allows(rbac.IdentityManage, "") {
			denied = true
			return audit(ctx, tx, now, p.ID, "user.grants.read", id, "", "denied", "forbidden")
		}
		if _, err := principal(tx, id); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		grants, err = grantsFor(tx, "user_id", id)
		return err
	})
	if err != nil {
		return nil, err
	}
	if denied {
		return nil, ErrDenied
	}
	return grants, nil
}

func (s *Store) SetUserGrants(ctx context.Context, token, csrf, id string, grants []rbac.Grant, now time.Time) error {
	if grants == nil {
		return ErrInput
	}
	return s.accountMutation(ctx, token, csrf, "user.grants.replace", id, now, func(tx *sql.Tx, p rbac.Principal) error {
		if !p.Allows(rbac.IdentityManage, "") {
			return ErrDenied
		}
		target, err := principal(tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// Ownership changes use a separate, reauthenticated operation.
		if owner(target) {
			return ErrDenied
		}
		if err := validateGrants(tx, p, grants); err != nil {
			return err
		}
		if err := replaceGrants(tx, "user_id", id, grants); err != nil {
			return err
		}
		return bumpAuthorization(tx, []string{id}, now)
	})
}
