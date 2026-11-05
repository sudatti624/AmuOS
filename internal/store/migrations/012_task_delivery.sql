-- +goose Up
CREATE TABLE task_attempts (
 task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
 attempt_id TEXT NOT NULL UNIQUE,
 agent_id TEXT NOT NULL REFERENCES agents(id),
 input_hash TEXT NOT NULL,
 outcome TEXT CHECK(outcome IN ('succeeded','failed','unknown'))
);
CREATE TABLE systemd_states (
 host_id TEXT PRIMARY KEY REFERENCES hosts(id),
 agent_id TEXT NOT NULL REFERENCES agents(id),
 collected_at INTEGER NOT NULL,
 received_at INTEGER NOT NULL,
 payload TEXT NOT NULL
);
