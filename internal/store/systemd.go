package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/sudatti624/AmuOS/internal/metrics"
	"time"
)

type SystemdRecord struct {
	metrics.SystemdSnapshot
	ReceivedAt int64 `json:"received_at"`
}

func (s *Store) RecordSystemd(ctx context.Context, serial, agentID string, value metrics.SystemdSnapshot, now time.Time) error {
	if value.Validate() != nil || value.CollectedAt > now.Add(clockSkew).Unix() || value.CollectedAt < now.Add(-sampleMaxAge).Unix() {
		return ErrInput
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		p, err := agentAuthenticate(ctx, tx, serial, agentID, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO systemd_states(host_id,agent_id,collected_at,received_at,payload) VALUES(?,?,?,?,?) ON CONFLICT(host_id) DO UPDATE SET agent_id=excluded.agent_id,collected_at=excluded.collected_at,received_at=excluded.received_at,payload=excluded.payload WHERE excluded.collected_at>=systemd_states.collected_at`, p.HostID, p.AgentID, value.CollectedAt, now.Unix(), string(encoded))
		return err
	})
}
func (s *Store) Systemd(ctx context.Context, token, host string, now time.Time) (*SystemdRecord, error) {
	var result *SystemdRecord
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		state, err := hostState(tx, p, host)
		if err != nil {
			return err
		}
		if state == "" {
			return ErrNotFound
		}
		var record SystemdRecord
		var payload string
		err = tx.QueryRow("SELECT payload,received_at FROM systemd_states WHERE host_id=?", host).Scan(&payload, &record.ReceivedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(payload), &record.SystemdSnapshot); err != nil {
			return err
		}
		result = &record
		return nil
	})
	return result, err
}
