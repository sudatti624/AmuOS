-- +goose Up
ALTER TABLE audit_events ADD COLUMN request_id TEXT NOT NULL DEFAULT '';
CREATE INDEX audit_events_request ON audit_events(request_id,id);
