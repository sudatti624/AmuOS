package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	"modernc.org/sqlite"
)

func SchemaVersion() int { return len(migrationNames) }

func (s *Store) BackupCounts(ctx context.Context) (map[string]int64, error) {
	counts := map[string]int64{}
	for _, table := range []string{"users", "groups", "memberships", "grants", "sessions", "hosts", "invitations", "invitation_grants", "bootstrap", "audit_events", "audit_settings", "idempotency_records", "local_maintenance"} {
		var count int64
		// Table identifiers are a compile-time allowlist, never external input.
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return nil, err
		}
		counts[table] = count
	}
	return counts, nil
}

// Snapshot uses SQLite's backup API, including committed WAL contents. The
// caller must serialize all writers with the state-directory lock.
func (s *Store) Snapshot(ctx context.Context, destination string) error {
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	connection, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Raw(func(raw any) (result error) {
		backup, err := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		}).NewBackup(destination)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, backup.Finish()) }()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(128)
			if err != nil || !more {
				return err
			}
		}
	})
}

// ReplaceFrom uses a SQLite backup transaction rather than replacing database
// files beside a potentially live WAL. SQLite rolls an interrupted transaction
// back during recovery; a completed transaction contains the whole new state.
func (s *Store) ReplaceFrom(ctx context.Context, source string) error {
	connection, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	return connection.Raw(func(raw any) (result error) {
		backup, err := raw.(interface {
			NewRestore(string) (*sqlite.Backup, error)
		}).NewRestore(source)
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, backup.Finish()) }()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(128)
			if err != nil || !more {
				return err
			}
		}
	})
}

// ValidateSnapshot does not create, migrate, or write to the inspected DB.
// Exact schema comparison also rejects triggers/views smuggled into an archive.
func ValidateSnapshot(ctx context.Context, path string) error {
	db, err := openReadOnly(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != SchemaVersion() {
		return errors.New("backup schema does not match this binary")
	}
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return errors.New("backup database integrity check failed")
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if invalid {
		return errors.New("backup database foreign key check failed")
	}
	reference, err := Open(":memory:")
	if err != nil {
		return err
	}
	defer reference.Close()
	readSchema := func(db *sql.DB) ([]string, error) {
		rows, err := db.QueryContext(ctx, "SELECT type,name,tbl_name,coalesce(sql,'') FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*' ORDER BY type,name")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var schema []string
		for rows.Next() {
			var kind, name, table, definition string
			if err := rows.Scan(&kind, &name, &table, &definition); err != nil {
				return nil, err
			}
			schema = append(schema, fmt.Sprintf("%s\x00%s\x00%s\x00%s", kind, name, table, definition))
		}
		return schema, rows.Err()
	}
	actual, err := readSchema(db)
	if err != nil {
		return err
	}
	expected, err := readSchema(reference.db)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("backup database schema is not recognized")
	}
	return nil
}

func (s *Store) StartLocalMaintenance(ctx context.Context, id, operation string, now time.Time, details map[string]any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO local_maintenance(id,operation,state,started_at,details) VALUES(?,?,'running',?,?)", id, operation, now.Unix(), string(encoded))
		if err != nil {
			return err
		}
		return auditWithDetails(ctx, tx, now, "local", operation+".start", id, "", "success", "", details)
	})
}

func (s *Store) FinishLocalMaintenance(ctx context.Context, id, state string, now time.Time) error {
	return s.FinishLocalMaintenanceDetails(ctx, id, state, now, nil)
}

func (s *Store) FinishLocalMaintenanceDetails(ctx context.Context, id, state string, now time.Time, details map[string]any) error {
	if state != "succeeded" && state != "failed" {
		return ErrInput
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var operation string
		if err := tx.QueryRowContext(ctx, "SELECT operation FROM local_maintenance WHERE id=? AND state='running'", id).Scan(&operation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE local_maintenance SET state=?,finished_at=? WHERE id=?", state, now.Unix(), id); err != nil {
			return err
		}
		if details != nil {
			encoded, err := json.Marshal(details)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE local_maintenance SET details=? WHERE id=?", string(encoded), id); err != nil {
				return err
			}
		}
		return auditWithDetails(ctx, tx, now, "local", operation+".finish", id, "", state, "", details)
	})
}

// RecoverLocalMaintenance never guesses that an interrupted backup succeeded.
func (s *Store) RecoverLocalMaintenance(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id,operation FROM local_maintenance WHERE state='running'")
		if err != nil {
			return err
		}
		type run struct{ id, operation string }
		var interrupted []run
		for rows.Next() {
			var r run
			if err := rows.Scan(&r.id, &r.operation); err != nil {
				rows.Close()
				return err
			}
			interrupted = append(interrupted, r)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		for _, r := range interrupted {
			if _, err := tx.ExecContext(ctx, "UPDATE local_maintenance SET state='unknown',finished_at=? WHERE id=?", now.Unix(), r.id); err != nil {
				return err
			}
			if err := audit(ctx, tx, now, "local", r.operation+".recover", r.id, "", "unknown", "interrupted"); err != nil {
				return err
			}
		}
		return nil
	})
}

// PrepareRestoredState runs only on the validated isolated staging DB, before
// it is published. All old browser and setup credentials become unusable.
func (s *Store) PrepareRestoredState(ctx context.Context, id string, now time.Time, details map[string]any) error {
	if err := s.RecoverLocalMaintenance(ctx, now); err != nil {
		return err
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := auditWithDetails(ctx, tx, now, "local", "restore.start", id, "", "success", "validated", details); err != nil {
			return err
		}
		for _, query := range []string{
			"DELETE FROM sessions", "DELETE FROM bootstrap",
			"UPDATE invitations SET revoked_at=coalesce(revoked_at,?) WHERE consumed_at IS NULL",
			"UPDATE users SET authorization_version=authorization_version+1",
		} {
			var err error
			if query == "UPDATE invitations SET revoked_at=coalesce(revoked_at,?) WHERE consumed_at IS NULL" {
				_, err = tx.ExecContext(ctx, query, now.Unix())
			} else {
				_, err = tx.ExecContext(ctx, query)
			}
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO local_maintenance(id,operation,state,started_at,finished_at,details) VALUES(?,'restore','succeeded',?,?,?)", id, now.Unix(), now.Unix(), string(encoded)); err != nil {
			return err
		}
		return auditWithDetails(ctx, tx, now, "local", "restore.complete", id, "", "succeeded", "credentials_invalidated", details)
	})
}
