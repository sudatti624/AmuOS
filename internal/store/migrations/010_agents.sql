-- +goose Up
CREATE TABLE agents (
 id TEXT PRIMARY KEY,
 host_id TEXT NOT NULL REFERENCES hosts(id),
 public_key_hash BLOB NOT NULL CHECK(length(public_key_hash)=32),
 state TEXT NOT NULL CHECK(state IN ('active','revoked','deleted')),
 created_at INTEGER NOT NULL,
 last_seen INTEGER,
 version TEXT NOT NULL DEFAULT '',
 capabilities TEXT NOT NULL DEFAULT '[]'
);
CREATE UNIQUE INDEX agents_one_active ON agents(host_id) WHERE state='active';
CREATE TABLE agent_credentials (
 serial TEXT PRIMARY KEY,
 agent_id TEXT NOT NULL REFERENCES agents(id),
 public_key_hash BLOB NOT NULL CHECK(length(public_key_hash)=32),
 cert_pem TEXT NOT NULL,
 issued_at INTEGER NOT NULL,
 not_after INTEGER NOT NULL,
 revoked_at INTEGER,
 replaces TEXT REFERENCES agent_credentials(serial),
 retire_at INTEGER
);
CREATE INDEX agent_credentials_agent ON agent_credentials(agent_id);
CREATE UNIQUE INDEX agent_credentials_successor ON agent_credentials(replaces) WHERE replaces IS NOT NULL;
CREATE TABLE enrollments (
 id TEXT PRIMARY KEY,
 host_id TEXT NOT NULL REFERENCES hosts(id),
 token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash)=32),
 issuer_id TEXT NOT NULL REFERENCES users(id),
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 consumed_at INTEGER,
 revoked_at INTEGER,
 public_key BLOB,
 agent_id TEXT REFERENCES agents(id),
 CHECK((consumed_at IS NULL) = (agent_id IS NULL)),
 CHECK((consumed_at IS NULL) = (public_key IS NULL))
);
CREATE INDEX enrollments_host ON enrollments(host_id);
CREATE TABLE enrollment_challenges (
 id TEXT PRIMARY KEY,
 enrollment_id TEXT NOT NULL REFERENCES enrollments(id),
 nonce_hash BLOB NOT NULL CHECK(length(nonce_hash)=32),
 expires_at INTEGER NOT NULL,
 consumed_at INTEGER
);
CREATE INDEX enrollment_challenges_expiry ON enrollment_challenges(expires_at);
