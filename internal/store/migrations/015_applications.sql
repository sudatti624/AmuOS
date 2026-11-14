-- +goose Up
CREATE TABLE applications (
 host_id TEXT NOT NULL REFERENCES hosts(id),
 name TEXT NOT NULL,
 config TEXT NOT NULL CHECK(json_valid(config)),
 revision INTEGER NOT NULL CHECK(revision > 0),
 archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0,1)),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(host_id,name)
);
