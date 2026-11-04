-- +goose Up
ALTER TABLE audit_events ADD COLUMN details TEXT NOT NULL DEFAULT '{}';
CREATE INDEX audit_events_time ON audit_events(at,id);
CREATE TABLE audit_settings (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 retention_days INTEGER NOT NULL CHECK(retention_days BETWEEN 1 AND 3650)
);
INSERT INTO audit_settings(singleton,retention_days) VALUES(1,180);
