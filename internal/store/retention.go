package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
)

const auditRetentionBatch = 1000

type AuditRetention struct {
	Days      int    `json:"retention_days"`
	Automatic bool   `json:"automatic_enabled"`
	NextRun   int64  `json:"next_run_at"`
	LastRun   *int64 `json:"last_run_at"`
	Failures  int    `json:"failure_count"`
	LastError string `json:"last_error_code,omitempty"`
}
type AuditPurge struct {
	Days    int   `json:"retention_days"`
	Cutoff  int64 `json:"cutoff"`
	Deleted int64 `json:"deleted_count"`
	FirstID int64 `json:"first_deleted_id"`
	LastID  int64 `json:"last_deleted_id"`
	More    bool  `json:"more_expired"`
}

func (s *Store) AuditRetention(ctx context.Context, token string, now time.Time) (AuditRetention, error) {
	var settings AuditRetention
	var denied bool
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !p.Allows(rbac.AuditRead, "") {
			denied = true
			return audit(ctx, tx, now, p.ID, "audit.retention.read", "audit", "", "denied", "forbidden")
		}
		return tx.QueryRow("SELECT retention_days,automatic_enabled,next_run_at,last_run_at,failure_count,last_error_code FROM audit_settings WHERE singleton=1").Scan(&settings.Days, &settings.Automatic, &settings.NextRun, &settings.LastRun, &settings.Failures, &settings.LastError)
	})
	if err != nil {
		return AuditRetention{}, err
	}
	if denied {
		return AuditRetention{}, ErrDenied
	}
	return settings, nil
}

func (s *Store) SetAuditRetention(ctx context.Context, token, csrf string, days int, now time.Time) error {
	return s.SetAuditPolicy(ctx, token, csrf, days, nil, now)
}

func (s *Store) SetAuditPolicy(ctx context.Context, token, csrf string, days int, automatic *bool, now time.Time) error {
	if days < 1 || days > 3650 {
		return ErrInput
	}
	details := map[string]any{}
	return s.accountMutationDetails(ctx, token, csrf, "audit.retention.set", "audit", now, details, func(tx *sql.Tx, p rbac.Principal) error {
		if err := requireRecentOwner(tx, p, token, now); err != nil {
			return err
		}
		var previous int
		var wasAutomatic bool
		if err := tx.QueryRow("SELECT retention_days,automatic_enabled FROM audit_settings WHERE singleton=1").Scan(&previous, &wasAutomatic); err != nil {
			return err
		}
		details["before_days"] = previous
		details["after_days"] = days
		enabled := wasAutomatic
		if automatic != nil {
			enabled = *automatic
		}
		details["before_automatic"] = wasAutomatic
		details["after_automatic"] = enabled
		_, err := tx.Exec("UPDATE audit_settings SET retention_days=?,automatic_enabled=?,next_run_at=0 WHERE singleton=1", days, enabled)
		return err
	})
}

func (s *Store) PurgeAudit(ctx context.Context, token, csrf string, now time.Time) (AuditPurge, error) {
	result := AuditPurge{}
	details := map[string]any{}
	err := s.accountMutationDetails(ctx, token, csrf, "audit.retention.run", "audit", now, details, func(tx *sql.Tx, p rbac.Principal) error {
		if err := requireRecentOwner(tx, p, token, now); err != nil {
			return err
		}
		var info map[string]any
		var err error
		result, info, err = purgeAuditRows(tx, now)
		if err != nil {
			return err
		}
		for key, value := range info {
			details[key] = value
		}
		return nil
	})
	if err != nil {
		return AuditPurge{}, err
	}
	return result, nil
}

func purgeAuditRows(tx *sql.Tx, now time.Time) (AuditPurge, map[string]any, error) {
	result := AuditPurge{}
	details := map[string]any{}
	if err := tx.QueryRow("SELECT retention_days FROM audit_settings WHERE singleton=1").Scan(&result.Days); err != nil {
		return AuditPurge{}, nil, err
	}
	result.Cutoff = now.UTC().Add(-time.Duration(result.Days) * 24 * time.Hour).Unix()
	if err := tx.QueryRow(`SELECT count(*),coalesce(min(id),0),coalesce(max(id),0) FROM (SELECT id FROM audit_events WHERE at<? ORDER BY id LIMIT ?)`, result.Cutoff, auditRetentionBatch).Scan(&result.Deleted, &result.FirstID, &result.LastID); err != nil {
		return AuditPurge{}, nil, err
	}
	deleted, err := tx.Exec("DELETE FROM audit_events WHERE id IN (SELECT id FROM audit_events WHERE at<? ORDER BY id LIMIT ?)", result.Cutoff, auditRetentionBatch)
	if err != nil {
		return AuditPurge{}, nil, err
	}
	affected, err := deleted.RowsAffected()
	if err != nil {
		return AuditPurge{}, nil, err
	}
	if affected != result.Deleted {
		return AuditPurge{}, nil, errors.New("audit retention batch changed")
	}
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM audit_events WHERE at<?)", result.Cutoff).Scan(&result.More); err != nil {
		return AuditPurge{}, nil, err
	}
	details["retention_days"] = result.Days
	details["cutoff"] = result.Cutoff
	details["deleted_count"] = result.Deleted
	details["first_deleted_id"] = result.FirstID
	details["last_deleted_id"] = result.LastID
	details["more_expired"] = result.More
	details["batch_limit"] = auditRetentionBatch
	return result, details, nil
}
