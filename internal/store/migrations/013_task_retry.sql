-- +goose Up
-- A re-run is a new task that keeps the ID of the task it re-runs. There is
-- no foreign key, so the reference survives the original's retention.
ALTER TABLE tasks ADD COLUMN retry_of TEXT;
CREATE UNIQUE INDEX tasks_retry_of ON tasks(retry_of) WHERE retry_of IS NOT NULL;
