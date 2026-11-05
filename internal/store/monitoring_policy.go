package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
)

type MonitoringPolicy struct {
	SampleInterval int `json:"sample_interval_seconds"`
	RawDays        int `json:"raw_retention_days"`
	RollupDays     int `json:"rollup_retention_days"`
}

func readMonitoringPolicy(tx *sql.Tx) (MonitoringPolicy, error) {
	var p MonitoringPolicy
	err := tx.QueryRow("SELECT sample_interval_seconds,raw_retention_days,rollup_retention_days FROM monitoring_settings WHERE singleton=1").Scan(&p.SampleInterval, &p.RawDays, &p.RollupDays)
	return p, err
}

func (s *Store) MonitoringPolicy(ctx context.Context, token string, now time.Time) (MonitoringPolicy, error) {
	var settings MonitoringPolicy
	denied := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if !p.Allows(rbac.IdentityManage, "") {
			denied = true
			return audit(ctx, tx, now, p.ID, "monitoring.settings.read", "monitoring", "", "denied", "forbidden")
		}
		settings, err = readMonitoringPolicy(tx)
		return err
	})
	if err != nil {
		return MonitoringPolicy{}, err
	}
	if denied {
		return MonitoringPolicy{}, ErrDenied
	}
	return settings, nil
}

func (s *Store) SetMonitoringPolicy(ctx context.Context, token, csrf string, settings MonitoringPolicy, now time.Time) error {
	if settings.SampleInterval < 5 || settings.SampleInterval > 300 || settings.RawDays < 1 || settings.RawDays > 30 || settings.RollupDays < 7 || settings.RollupDays > 400 || settings.RollupDays < settings.RawDays {
		return ErrInput
	}
	details := map[string]any{}
	return s.accountMutationDetails(ctx, token, csrf, "monitoring.settings.set", "monitoring", now, details, func(tx *sql.Tx, p rbac.Principal) error {
		if !p.Allows(rbac.IdentityManage, "") {
			return ErrDenied
		}
		previous, err := readMonitoringPolicy(tx)
		if err != nil {
			return err
		}
		details["before"] = previous
		details["after"] = settings
		_, err = tx.Exec("UPDATE monitoring_settings SET sample_interval_seconds=?,raw_retention_days=?,rollup_retention_days=? WHERE singleton=1", settings.SampleInterval, settings.RawDays, settings.RollupDays)
		return err
	})
}
