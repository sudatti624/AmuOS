package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/sudatti624/AmuOS/internal/metrics"
)

const (
	// Agents keep unsent samples for 10 minutes; one more covers transit.
	sampleMaxAge = 11 * time.Minute
	clockSkew    = time.Minute
	rollupStep   = 300
	// A bucket is aggregated only once no accepted sample can still land in it.
	rollupDelay = sampleMaxAge + 5*time.Minute
	rollupChunk = 288
	day         = 86400
)

type SampleResult struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
	Expired    int `json:"expired"`
}

// RecordSamples stores a batch from one Agent run. The host comes from the
// credential, never from the request. A repeated (run, seq) is a duplicate,
// and samples too old to aggregate correctly are counted as expired. A
// sample from the future means the Agent's clock is wrong, so the whole
// batch is refused.
func (s *Store) RecordSamples(ctx context.Context, serial, agentID, runID string, samples []metrics.Sample, now time.Time) (SampleResult, error) {
	var result SampleResult
	for _, sample := range samples {
		if sample.Validate() != nil || sample.CollectedAt > now.Add(clockSkew).Unix() {
			return result, ErrInput
		}
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		result = SampleResult{}
		p, err := agentAuthenticate(ctx, tx, serial, agentID, now)
		if err != nil {
			return err
		}
		var rolled int64
		if err := tx.QueryRow("SELECT rolled_up_until FROM monitoring_settings WHERE singleton=1").Scan(&rolled); err != nil {
			return err
		}
		oldest := max(now.Add(-sampleMaxAge).Unix(), rolled)
		insert, err := tx.Prepare(`INSERT INTO metric_samples(agent_id,run_id,seq,host_id,collected_at,received_at,cpu,load1,load5,load15,memory,swap,disk,disk_read,disk_write,net_rx,net_tx,uptime)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`)
		if err != nil {
			return err
		}
		defer insert.Close()
		for _, sample := range samples {
			if sample.CollectedAt < oldest {
				result.Expired++
				continue
			}
			args := []any{p.AgentID, runID, sample.Seq, p.HostID, sample.CollectedAt, now.Unix()}
			for _, v := range sample.Values() {
				args = append(args, v)
			}
			args = append(args, sample.Uptime)
			inserted, err := insert.Exec(args...)
			if err != nil {
				return err
			}
			if count, err := inserted.RowsAffected(); err != nil {
				return err
			} else if count == 0 {
				result.Duplicates++
			} else {
				result.Accepted++
			}
		}
		_, err = tx.Exec("UPDATE agents SET last_seen=? WHERE id=?", now.Unix(), p.AgentID)
		return err
	})
	return result, err
}

// RecordHostInfo keeps the newest host information an Agent has sent.
func (s *Store) RecordHostInfo(ctx context.Context, serial, agentID string, info metrics.HostInfo, now time.Time) error {
	if info.Validate() != nil || info.CollectedAt > now.Add(clockSkew).Unix() {
		return ErrInput
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		p, err := agentAuthenticate(ctx, tx, serial, agentID, now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO host_info(host_id,agent_id,info,collected_at,received_at) VALUES(?,?,?,?,?)
			ON CONFLICT(host_id) DO UPDATE SET agent_id=excluded.agent_id,info=excluded.info,collected_at=excluded.collected_at,received_at=excluded.received_at
			WHERE excluded.collected_at>=host_info.collected_at`, p.HostID, p.AgentID, string(encoded), info.CollectedAt, now.Unix()); err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE agents SET last_seen=? WHERE id=?", now.Unix(), p.AgentID)
		return err
	})
}

type HostInfoRecord struct {
	metrics.HostInfo
	ReceivedAt int64 `json:"received_at"`
}

type LatestSample struct {
	metrics.Sample
	ReceivedAt int64 `json:"received_at"`
	// Current is false once the value is too old to show as the present
	// state; the UI then labels it with its time instead.
	Current bool `json:"current"`
}

type HostDetail struct {
	Host
	Info           *HostInfoRecord `json:"info"`
	Latest         *LatestSample   `json:"latest"`
	SampleInterval int64           `json:"sample_interval"`
}

func sampleColumns() string { return strings.Join(metrics.Names, ",") }

func (s *Store) HostDetail(ctx context.Context, token, id string, now time.Time) (HostDetail, error) {
	var detail HostDetail
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		state, err := hostState(tx, p, id)
		if err != nil {
			return err
		}
		if state == "" {
			return ErrNotFound
		}
		detail = HostDetail{Host: Host{ID: id, State: state}}
		if err := tx.QueryRow("SELECT name FROM hosts WHERE id=?", id).Scan(&detail.Name); err != nil {
			return err
		}
		if detail.Agent, err = agentInfo(tx, id, now); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT sample_interval_seconds FROM monitoring_settings WHERE singleton=1").Scan(&detail.SampleInterval); err != nil {
			return err
		}
		var info string
		var received int64
		err = tx.QueryRow("SELECT info,received_at FROM host_info WHERE host_id=?", id).Scan(&info, &received)
		switch {
		case err == nil:
			detail.Info = &HostInfoRecord{ReceivedAt: received}
			if err := json.Unmarshal([]byte(info), &detail.Info.HostInfo); err != nil {
				return err
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var latest LatestSample
		targets := []any{&latest.Seq, &latest.CollectedAt, &latest.ReceivedAt}
		values := make([]*float64, len(metrics.Names))
		for i := range values {
			targets = append(targets, &values[i])
		}
		targets = append(targets, &latest.Uptime)
		err = tx.QueryRow("SELECT seq,collected_at,received_at,"+sampleColumns()+",uptime FROM metric_samples WHERE host_id=? ORDER BY collected_at DESC,received_at DESC LIMIT 1", id).Scan(targets...)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		v := values
		latest.CPU, latest.Load1, latest.Load5, latest.Load15, latest.Memory, latest.Swap, latest.Disk = v[0], v[1], v[2], v[3], v[4], v[5], v[6]
		latest.DiskRead, latest.DiskWrite, latest.NetRx, latest.NetTx = v[7], v[8], v[9], v[10]
		latest.Current = state == "active" && now.Unix()-latest.ReceivedAt < int64(disconnectedAfter/time.Second)
		detail.Latest = &latest
		return nil
	})
	return detail, err
}

type historyRange struct{ duration, step int64 }

// Ranges a history request may ask for, each with its display step.
var historyRanges = map[string]historyRange{
	"1h":  {3600, 15},
	"6h":  {6 * 3600, 60},
	"24h": {day, 300},
	"7d":  {7 * day, 1800},
	"30d": {30 * day, 7200},
}

func ValidHistoryRange(name string) bool { _, ok := historyRanges[name]; return ok }

// Series holds one value per entry of History.Times; nil means no sample in
// that step had the value.
type Series struct {
	Avg []*float64 `json:"avg"`
	Min []*float64 `json:"min"`
	Max []*float64 `json:"max"`
}

// History lists only the steps that have data. A step missing from Times is
// a gap, and the UI must show it as one.
type History struct {
	Range  string             `json:"range"`
	Step   int64              `json:"step"`
	From   int64              `json:"from"`
	To     int64              `json:"to"`
	Times  []int64            `json:"times"`
	Series map[string]*Series `json:"series"`
}

type aggregate struct {
	count    int64
	min, max float64
	sum      float64
}

func (a *aggregate) merge(count int64, lo, hi, sum float64) {
	if count == 0 {
		return
	}
	if a.count == 0 || lo < a.min {
		a.min = lo
	}
	if a.count == 0 || hi > a.max {
		a.max = hi
	}
	a.count += count
	a.sum += sum
}

// MetricHistory reads raw samples where they are kept and 5-minute rollups
// before that, so each time range is counted exactly once.
func (s *Store) MetricHistory(ctx context.Context, token, id, rangeName string, now time.Time) (History, error) {
	r, ok := historyRanges[rangeName]
	if !ok {
		return History{}, ErrInput
	}
	to := now.Unix() + 1
	from := (now.Unix() - r.duration) / r.step * r.step
	history := History{Range: rangeName, Step: r.step, From: from, To: now.Unix(), Times: []int64{}, Series: map[string]*Series{}}
	buckets := map[int64][]aggregate{}
	slot := func(k int64) []aggregate {
		if buckets[k] == nil {
			buckets[k] = make([]aggregate, len(metrics.Names))
		}
		return buckets[k]
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if state, err := hostState(tx, p, id); err != nil {
			return err
		} else if state == "" {
			return ErrNotFound
		}
		var rawDays, rolled int64
		if err := tx.QueryRow("SELECT raw_retention_days,rolled_up_until FROM monitoring_settings WHERE singleton=1").Scan(&rawDays, &rolled); err != nil {
			return err
		}
		// Raw rows are deleted only after they are rolled up, and only once
		// older than the raw retention. An hour of margin avoids racing that.
		boundary := min((now.Unix()-rawDays*day+3600)/rollupStep*rollupStep, rolled)
		var columns []string
		for _, name := range metrics.Names {
			columns = append(columns, "count("+name+"),min("+name+"),max("+name+"),sum("+name+")")
		}
		rows, err := tx.Query("SELECT (collected_at/?)*? AS k,"+strings.Join(columns, ",")+" FROM metric_samples WHERE host_id=? AND collected_at>=? AND collected_at<? GROUP BY k", r.step, r.step, id, max(from, boundary), to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k int64
			counts := make([]int64, len(metrics.Names))
			values := make([]sql.NullFloat64, 3*len(metrics.Names))
			targets := []any{&k}
			for i := range metrics.Names {
				targets = append(targets, &counts[i], &values[3*i], &values[3*i+1], &values[3*i+2])
			}
			if err := rows.Scan(targets...); err != nil {
				return err
			}
			a := slot(k)
			for i := range metrics.Names {
				a[i].merge(counts[i], values[3*i].Float64, values[3*i+1].Float64, values[3*i+2].Float64)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		if from >= boundary {
			return nil
		}
		index := map[string]int{}
		for i, name := range metrics.Names {
			index[name] = i
		}
		rows, err = tx.Query("SELECT (bucket/?)*? AS k,metric,sum(count),min(min),max(max),sum(avg*count) FROM metric_rollups WHERE host_id=? AND bucket>=? AND bucket<? GROUP BY k,metric", r.step, r.step, id, from, boundary)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k, count int64
			var metric string
			var lo, hi, sum float64
			if err := rows.Scan(&k, &metric, &count, &lo, &hi, &sum); err != nil {
				return err
			}
			if i, ok := index[metric]; ok {
				slot(k)[i].merge(count, lo, hi, sum)
			}
		}
		return rows.Err()
	})
	if err != nil {
		return History{}, err
	}
	for k := range buckets {
		history.Times = append(history.Times, k)
	}
	slices.Sort(history.Times)
	for i, name := range metrics.Names {
		series := &Series{Avg: []*float64{}, Min: []*float64{}, Max: []*float64{}}
		for _, k := range history.Times {
			a := buckets[k][i]
			if a.count == 0 {
				series.Avg, series.Min, series.Max = append(series.Avg, nil), append(series.Min, nil), append(series.Max, nil)
				continue
			}
			avg, lo, hi := a.sum/float64(a.count), a.min, a.max
			series.Avg, series.Min, series.Max = append(series.Avg, &avg), append(series.Min, &lo), append(series.Max, &hi)
		}
		history.Series[name] = series
	}
	return history, nil
}

// RunMonitoringMaintenance aggregates finished 5-minute buckets, a bounded
// number per call, then deletes raw samples that are both past retention and
// aggregated, and rollups past theirs. It is safe to repeat.
func (s *Store) RunMonitoringMaintenance(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var rolled, rawDays, rollupDays int64
		if err := tx.QueryRow("SELECT rolled_up_until,raw_retention_days,rollup_retention_days FROM monitoring_settings WHERE singleton=1").Scan(&rolled, &rawDays, &rollupDays); err != nil {
			return err
		}
		target := (now.Unix() - int64(rollupDelay/time.Second)) / rollupStep * rollupStep
		if rolled < target {
			var first sql.NullInt64
			if err := tx.QueryRow("SELECT min(collected_at) FROM metric_samples WHERE collected_at>=?", rolled).Scan(&first); err != nil {
				return err
			}
			start := rolled
			if !first.Valid {
				start = target
			} else if aligned := first.Int64 / rollupStep * rollupStep; aligned > start {
				start = min(aligned, target)
			}
			end := min(target, start+rollupChunk*rollupStep)
			if start < end {
				for _, name := range metrics.Names {
					// Metric names are a compile-time allowlist.
					if _, err := tx.Exec("INSERT OR REPLACE INTO metric_rollups(host_id,bucket,metric,count,min,max,avg) SELECT host_id,(collected_at/?)*?,?,count("+name+"),min("+name+"),max("+name+"),avg("+name+") FROM metric_samples WHERE collected_at>=? AND collected_at<? AND "+name+" IS NOT NULL GROUP BY host_id,(collected_at/?)*?",
						rollupStep, rollupStep, name, start, end, rollupStep, rollupStep); err != nil {
						return err
					}
				}
			}
			rolled = max(start, end)
			if _, err := tx.Exec("UPDATE monitoring_settings SET rolled_up_until=? WHERE singleton=1", rolled); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("DELETE FROM metric_samples WHERE collected_at<?", min(now.Unix()-rawDays*day, rolled)); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM metric_rollups WHERE bucket<?", now.Unix()-rollupDays*day)
		return err
	})
}
