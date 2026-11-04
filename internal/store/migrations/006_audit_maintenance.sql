-- +goose Up
ALTER TABLE audit_settings ADD COLUMN automatic_enabled INTEGER NOT NULL DEFAULT 1 CHECK(automatic_enabled IN (0,1));
ALTER TABLE audit_settings ADD COLUMN next_run_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE audit_settings ADD COLUMN last_run_at INTEGER;
ALTER TABLE audit_settings ADD COLUMN failure_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE audit_settings ADD COLUMN last_error_code TEXT NOT NULL DEFAULT '';
