package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/sudatti624/AmuOS/internal/security"
)

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(id)
	if err != nil || len(decoded) != 32 {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

func RequestID(ctx context.Context) string { id, _ := ctx.Value(requestIDKey{}).(string); return id }

// HTTP rejections do not refresh sessions or trust any identity from the body.
func (s *Store) RecordHTTPRejection(ctx context.Context, token, route, reason string, now time.Time) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		actor := "anonymous"
		if token != "" {
			var id string
			var last, expiry int64
			var enabled bool
			err := tx.QueryRow(`SELECT u.id,s.last_seen,s.expires_at,u.enabled FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=?`, security.Digest(token)).Scan(&id, &last, &expiry, &enabled)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && enabled && now.Unix() >= last && now.Unix() < expiry && now.Unix()-last < 1800 {
				actor = id
			}
		}
		return audit(ctx, tx, now, actor, "http.reject", route, "", "denied", reason)
	})
}
