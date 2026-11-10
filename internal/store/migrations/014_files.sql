-- +goose Up
CREATE TABLE tasks_v14 (
 id TEXT PRIMARY KEY,
 requester_id TEXT NOT NULL REFERENCES users(id),
 operation TEXT NOT NULL CHECK(operation IN ('collect','file')),
 host_id TEXT NOT NULL REFERENCES hosts(id),
 input_hash BLOB NOT NULL CHECK(length(input_hash)=32),
 key_hash BLOB NOT NULL CHECK(length(key_hash)=32),
 state TEXT NOT NULL CHECK(state IN ('queued','dispatching','running','succeeded','failed','timed_out','cancelled','unknown')),
 attempt INTEGER NOT NULL DEFAULT 0 CHECK(attempt BETWEEN 0 AND 3),
 accept_deadline INTEGER NOT NULL,
 run_deadline INTEGER,
 reason TEXT NOT NULL DEFAULT '',
 request_id TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 finished_at INTEGER,
 resolved_by TEXT REFERENCES users(id),
 retry_of TEXT,
 UNIQUE(requester_id,operation,host_id,key_hash),
 CHECK((state IN ('succeeded','failed','timed_out','cancelled')) = (finished_at IS NOT NULL))
);

INSERT INTO tasks_v14 SELECT * FROM tasks;
CREATE TEMP TABLE saved_attempts AS SELECT * FROM task_attempts;
DROP TABLE task_attempts;
DROP TABLE tasks;
ALTER TABLE tasks_v14 RENAME TO tasks;
CREATE INDEX tasks_open ON tasks(state,accept_deadline);
CREATE INDEX tasks_finished ON tasks(finished_at) WHERE finished_at IS NOT NULL;
CREATE INDEX tasks_host ON tasks(host_id,created_at);
CREATE UNIQUE INDEX tasks_retry_of ON tasks(retry_of) WHERE retry_of IS NOT NULL;
-- +goose Up
CREATE TABLE task_attempts (
 task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
 attempt_id TEXT NOT NULL UNIQUE,
 agent_id TEXT NOT NULL REFERENCES agents(id),
 input_hash TEXT NOT NULL,
 outcome TEXT CHECK(outcome IN ('succeeded','failed','unknown'))
);
INSERT INTO task_attempts SELECT * FROM saved_attempts;
DROP TABLE saved_attempts;
CREATE TABLE file_roots (
 host_id TEXT NOT NULL REFERENCES hosts(id),
 alias TEXT NOT NULL,
 policy TEXT NOT NULL,
 PRIMARY KEY(host_id,alias)
);
CREATE TABLE file_tasks (
 task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
 request TEXT NOT NULL,
 result TEXT,
 policy TEXT NOT NULL
);
