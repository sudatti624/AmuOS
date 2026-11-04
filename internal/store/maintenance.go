package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) RunMaintenance(ctx context.Context, now time.Time) error {
	_, auditErr := s.RunAuditMaintenance(ctx, now)
	keyErr := s.ExpireIdempotency(ctx, now)
	return errors.Join(auditErr, keyErr)
}

// The due-time claim, deletion, audit and schedule update commit together.
func (s *Store) RunAuditMaintenance(ctx context.Context, now time.Time) (bool, error) {
	ran := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		claim, err := tx.Exec("UPDATE audit_settings SET next_run_at=? WHERE singleton=1 AND automatic_enabled=1 AND next_run_at<=?", now.Add(time.Hour).Unix(), now.Unix())
		if err != nil {
			return err
		}
		count, err := claim.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		ran = true
		result, details, err := purgeAuditRows(tx, now)
		if err != nil {
			return err
		}
		next := now.Add(time.Hour)
		if result.More {
			next = now.Add(time.Minute)
		}
		if _, err := tx.Exec("UPDATE audit_settings SET last_run_at=?,next_run_at=?,failure_count=0,last_error_code='' WHERE singleton=1", now.Unix(), next.Unix()); err != nil {
			return err
		}
		details["automatic"] = true
		details["next_run_at"] = next.Unix()
		return auditWithDetails(ctx, tx, now, "system", "audit.retention.run", "audit", "", "success", "", details)
	})
	if err != nil && ctx.Err() == nil {
		failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		// Do not store database error text, which can contain sensitive values.
		_ = s.transaction(failureCtx, func(tx *sql.Tx) error {
			_, err := tx.Exec("UPDATE audit_settings SET failure_count=failure_count+1,last_error_code='maintenance_failed',next_run_at=? WHERE singleton=1", now.Add(5*time.Minute).Unix())
			return err
		})
	}
	return ran && err == nil, err
}
