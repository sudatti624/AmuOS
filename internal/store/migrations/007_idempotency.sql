-- +goose Up
CREATE TABLE idempotency_records (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 actor_id TEXT NOT NULL REFERENCES users(id),
 operation TEXT NOT NULL,
 scope TEXT NOT NULL,
 key_hash BLOB NOT NULL CHECK(length(key_hash)=32),
 input_hash BLOB,
 resource_id TEXT REFERENCES hosts(id),
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('succeeded','expired')),
 UNIQUE(actor_id,operation,scope,key_hash),
 CHECK((state='succeeded' AND input_hash IS NOT NULL AND resource_id IS NOT NULL) OR (state='expired' AND input_hash IS NULL AND resource_id IS NULL))
);
CREATE INDEX idempotency_expiry ON idempotency_records(state,expires_at,id);
