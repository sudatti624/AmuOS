-- +goose Up
CREATE TABLE tasks (
 id TEXT PRIMARY KEY,
 requester_id TEXT NOT NULL REFERENCES users(id),
 operation TEXT NOT NULL CHECK(operation IN ('collect')),
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
 UNIQUE(requester_id,operation,host_id,key_hash),
 CHECK((state IN ('succeeded','failed','timed_out','cancelled')) = (finished_at IS NOT NULL))
);
CREATE INDEX tasks_open ON tasks(state,accept_deadline);
CREATE INDEX tasks_finished ON tasks(finished_at) WHERE finished_at IS NOT NULL;
CREATE INDEX tasks_host ON tasks(host_id,created_at);
