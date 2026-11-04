package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/sudatti624/AmuOS/internal/security"
)

type idempotencyKeyType struct{}

func ValidIdempotencyKey(key string) bool {
	if len(key) < 16 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyType{}, key)
}
func idempotencyKey(ctx context.Context) string {
	key, _ := ctx.Value(idempotencyKeyType{}).(string)
	return key
}

func lookupHostOperation(tx *sql.Tx, actor, key, name string, now time.Time) (Host, bool, string, error) {
	if !ValidIdempotencyKey(key) {
		return Host{}, false, "", ErrInput
	}
	var input []byte
	var resource sql.NullString
	var created, expires int64
	var state string
	err := tx.QueryRow("SELECT input_hash,resource_id,created_at,expires_at,state FROM idempotency_records WHERE actor_id=? AND operation='host.create' AND scope='host-registry' AND key_hash=?", actor, security.Digest(key)).Scan(&input, &resource, &created, &expires, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, false, "", nil
	}
	if err != nil {
		return Host{}, false, "", err
	}
	if state == "expired" || now.Unix() >= expires {
		return Host{}, true, "idempotency_expired", nil
	}
	if now.Unix() < created {
		return Host{}, true, "clock_regressed", nil
	}
	if !security.EqualDigest(name, input) {
		return Host{}, true, "idempotency_input_conflict", nil
	}
	var current string
	err = tx.QueryRow("SELECT state FROM hosts WHERE id=?", resource.String).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) || current == "deleted" || current == "revoked" {
		return Host{}, true, "resource_unavailable", nil
	}
	if err != nil {
		return Host{}, false, "", err
	}
	return Host{ID: resource.String, Name: name, State: "pending", Replayed: true}, true, "", nil
}

func recordHostOperation(tx *sql.Tx, actor, key string, host Host, now time.Time) error {
	_, err := tx.Exec("INSERT INTO idempotency_records(actor_id,operation,scope,key_hash,input_hash,resource_id,created_at,expires_at,state) VALUES(?,'host.create','host-registry',?,?,?,?,?,'succeeded')", actor, security.Digest(key), security.Digest(host.Name), host.ID, now.Unix(), now.Add(30*24*time.Hour).Unix())
	return err
}

func (s *Store) ExpireIdempotency(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		result, err := tx.Exec("UPDATE idempotency_records SET state='expired',input_hash=NULL,resource_id=NULL WHERE id IN (SELECT id FROM idempotency_records WHERE state='succeeded' AND expires_at<=? ORDER BY id LIMIT 1000)", now.Unix())
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count == 0 {
			return err
		}
		return auditWithDetails(ctx, tx, now, "system", "idempotency.expire", "host-registry", "", "success", "", map[string]any{"compacted_count": count, "cutoff": now.Unix()})
	})
}
