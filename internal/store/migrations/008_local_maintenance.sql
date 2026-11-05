-- +goose Up
CREATE TABLE local_maintenance (
 id TEXT PRIMARY KEY,
 operation TEXT NOT NULL CHECK(operation IN ('backup','restore')),
 state TEXT NOT NULL CHECK(state IN ('running','succeeded','failed','unknown')),
 started_at INTEGER NOT NULL,
 finished_at INTEGER,
 details TEXT NOT NULL
);
