-- +goose Up
ALTER TABLE sessions ADD COLUMN reauthenticated_at INTEGER;
CREATE TABLE invitations (
 id TEXT PRIMARY KEY,
 token_hash BLOB NOT NULL UNIQUE,
 username TEXT NOT NULL,
 issuer_id TEXT NOT NULL REFERENCES users(id),
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 revoked_at INTEGER,
 consumed_at INTEGER
);
CREATE UNIQUE INDEX invitations_pending_name ON invitations(username)
 WHERE revoked_at IS NULL AND consumed_at IS NULL;
CREATE TABLE invitation_grants (
 invitation_id TEXT NOT NULL REFERENCES invitations(id),
 role TEXT NOT NULL CHECK(role IN ('Admin','Operator','Viewer')),
 all_hosts INTEGER NOT NULL CHECK(all_hosts IN (0,1)),
 host_id TEXT REFERENCES hosts(id),
 CHECK((all_hosts=1 AND host_id IS NULL) OR (all_hosts=0 AND host_id IS NOT NULL))
);
