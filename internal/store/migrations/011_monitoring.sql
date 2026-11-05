-- +goose Up
CREATE TABLE monitoring_settings (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 sample_interval_seconds INTEGER NOT NULL DEFAULT 15 CHECK(sample_interval_seconds BETWEEN 5 AND 300),
 raw_retention_days INTEGER NOT NULL DEFAULT 7 CHECK(raw_retention_days BETWEEN 1 AND 30),
 rollup_retention_days INTEGER NOT NULL DEFAULT 30 CHECK(rollup_retention_days BETWEEN 7 AND 400),
 -- Every 5-minute bucket before this time has been aggregated.
 rolled_up_until INTEGER NOT NULL DEFAULT 0
);
INSERT INTO monitoring_settings(singleton) VALUES(1);
CREATE TABLE host_info (
 host_id TEXT PRIMARY KEY REFERENCES hosts(id),
 agent_id TEXT NOT NULL REFERENCES agents(id),
 info TEXT NOT NULL,
 collected_at INTEGER NOT NULL,
 received_at INTEGER NOT NULL
);
CREATE TABLE metric_samples (
 agent_id TEXT NOT NULL REFERENCES agents(id),
 run_id TEXT NOT NULL,
 seq INTEGER NOT NULL,
 host_id TEXT NOT NULL REFERENCES hosts(id),
 collected_at INTEGER NOT NULL,
 received_at INTEGER NOT NULL,
 cpu REAL, load1 REAL, load5 REAL, load15 REAL, memory REAL, swap REAL, disk REAL,
 disk_read REAL, disk_write REAL, net_rx REAL, net_tx REAL,
 uptime INTEGER,
 PRIMARY KEY(agent_id, run_id, seq)
) WITHOUT ROWID;
CREATE INDEX metric_samples_host_time ON metric_samples(host_id, collected_at);
CREATE INDEX metric_samples_time ON metric_samples(collected_at);
CREATE TABLE metric_rollups (
 host_id TEXT NOT NULL REFERENCES hosts(id),
 bucket INTEGER NOT NULL,
 metric TEXT NOT NULL,
 count INTEGER NOT NULL CHECK(count>0),
 min REAL NOT NULL,
 max REAL NOT NULL,
 avg REAL NOT NULL,
 PRIMARY KEY(host_id, bucket, metric)
) WITHOUT ROWID;
CREATE INDEX metric_rollups_bucket ON metric_rollups(bucket);
