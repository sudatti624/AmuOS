-- +goose Up
CREATE TABLE users (
 id TEXT PRIMARY KEY,
 username TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1))
);
CREATE TABLE groups (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE memberships (
 user_id TEXT NOT NULL REFERENCES users(id),
 group_id TEXT NOT NULL REFERENCES groups(id),
 PRIMARY KEY(user_id,group_id)
);
CREATE TABLE hosts (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','active','revoked','deleted'))
);
CREATE TABLE grants (
 id TEXT PRIMARY KEY,
 user_id TEXT REFERENCES users(id),
 group_id TEXT REFERENCES groups(id),
 role TEXT NOT NULL CHECK(role IN ('Owner','Admin','Operator','Viewer')),
 all_hosts INTEGER NOT NULL CHECK(all_hosts IN (0,1)),
 host_id TEXT REFERENCES hosts(id),
 CHECK((user_id IS NULL) != (group_id IS NULL)),
 CHECK((all_hosts=1 AND host_id IS NULL) OR (all_hosts=0 AND host_id IS NOT NULL)),
 CHECK(role!='Owner' OR (user_id IS NOT NULL AND all_hosts=1))
);
CREATE INDEX grants_user ON grants(user_id);
CREATE INDEX memberships_user ON memberships(user_id);
CREATE TABLE sessions (
 token_hash BLOB PRIMARY KEY,
 csrf_hash BLOB NOT NULL,
 user_id TEXT NOT NULL REFERENCES users(id),
 created_at INTEGER NOT NULL,
 last_seen INTEGER NOT NULL,
 expires_at INTEGER NOT NULL
);
CREATE TABLE bootstrap (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 token_hash BLOB NOT NULL,
 expires_at INTEGER NOT NULL
);
CREATE TABLE audit_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 at INTEGER NOT NULL,
 actor_id TEXT NOT NULL,
 action TEXT NOT NULL,
 target_id TEXT NOT NULL,
 host_id TEXT,
 outcome TEXT NOT NULL,
 reason TEXT NOT NULL
);
CREATE INDEX audit_events_host ON audit_events(host_id,id);
