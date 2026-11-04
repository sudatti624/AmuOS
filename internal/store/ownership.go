package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

func activeOwnerCount(tx *sql.Tx) (int, error) {
	var count int
	err := tx.QueryRow(`SELECT count(DISTINCT u.id) FROM users u JOIN grants g ON g.user_id=u.id WHERE u.enabled=1 AND g.role='Owner' AND g.all_hosts=1`).Scan(&count)
	return count, err
}

func (s *Store) SetOwnership(ctx context.Context, token, csrf, id string, enabled bool, now time.Time) error {
	action := "owner.remove"
	if enabled {
		action = "owner.add"
	}
	return s.accountMutation(ctx, token, csrf, action, id, now, func(tx *sql.Tx, p rbac.Principal) error {
		if err := requireRecentOwner(tx, p, token, now); err != nil {
			return err
		}
		target, err := principal(tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if enabled && !target.Enabled {
			return ErrConflict
		}
		if owner(target) == enabled {
			return nil
		}
		if !enabled && target.Enabled {
			count, err := activeOwnerCount(tx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrConflict
			}
		}
		if enabled {
			if _, err := tx.Exec("INSERT INTO grants(id,user_id,role,all_hosts) VALUES(?,?,'Owner',1)", security.Token(), id); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec("DELETE FROM grants WHERE user_id=? AND role='Owner'", id); err != nil {
				return err
			}
		}
		if err := bumpAuthorization(tx, []string{id}, now); err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", id)
		return err
	})
}

func (s *Store) TransferOwnership(ctx context.Context, token, csrf, targetID string, now time.Time) error {
	return s.accountMutation(ctx, token, csrf, "owner.transfer", targetID, now, func(tx *sql.Tx, p rbac.Principal) error {
		if err := requireRecentOwner(tx, p, token, now); err != nil {
			return err
		}
		if targetID == p.ID {
			return ErrConflict
		}
		target, err := principal(tx, targetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !target.Enabled {
			return ErrConflict
		}
		if !owner(target) {
			if _, err := tx.Exec("INSERT INTO grants(id,user_id,role,all_hosts) VALUES(?,?,'Owner',1)", security.Token(), targetID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("DELETE FROM grants WHERE user_id=? AND role='Owner'", p.ID); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM grants WHERE user_id=? AND role='Admin' AND all_hosts=1", p.ID).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := tx.Exec("INSERT INTO grants(id,user_id,role,all_hosts) VALUES(?,?,'Admin',1)", security.Token(), p.ID); err != nil {
				return err
			}
		}
		if err := bumpAuthorization(tx, []string{p.ID, targetID}, now); err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM sessions WHERE user_id IN (?,?)", p.ID, targetID)
		return err
	})
}
