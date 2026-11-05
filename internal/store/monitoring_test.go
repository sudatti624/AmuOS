package store

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/metrics"
	"github.com/sudatti624/AmuOS/internal/rbac"
)

const testRun = "run-0123456789abcdef"

func value(v float64) *float64 { return &v }

func sampleAt(seq int64, at time.Time, cpu float64) metrics.Sample {
	return metrics.Sample{Seq: seq, CollectedAt: at.Unix(), CPU: value(cpu), Memory: value(cpu / 2)}
}

func loginAt(t *testing.T, s *Store, at time.Time) Session {
	t.Helper()
	session, err := s.Login(context.Background(), "owner", testPassword, at)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestSamplesAreBoundToTheCredentialAndDeduplicated(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	agent := enrollAgent(t, s, owner, host, ca)
	id := agent.credential.AgentID
	now := testTime.Add(time.Minute)
	batch := []metrics.Sample{sampleAt(1, now.Add(-30*time.Second), 10), sampleAt(2, now.Add(-15*time.Second), 20)}
	result, err := s.RecordSamples(ctx, agent.serial, id, testRun, batch, now)
	if err != nil || result != (SampleResult{Accepted: 2}) {
		t.Fatalf("first batch: %+v %v", result, err)
	}
	// A retried batch after a lost response is stored once.
	result, err = s.RecordSamples(ctx, agent.serial, id, testRun, batch, now)
	if err != nil || result != (SampleResult{Duplicates: 2}) {
		t.Fatalf("retried batch: %+v %v", result, err)
	}
	// The same sequence number in a new run is a different sample.
	result, err = s.RecordSamples(ctx, agent.serial, id, "run-fedcba9876543210", batch[:1], now)
	if err != nil || result.Accepted != 1 {
		t.Fatalf("new run: %+v %v", result, err)
	}
	stale := sampleAt(3, now.Add(-12*time.Minute), 30)
	result, err = s.RecordSamples(ctx, agent.serial, id, testRun, []metrics.Sample{stale, sampleAt(4, now, 40)}, now)
	if err != nil || result != (SampleResult{Accepted: 1, Expired: 1}) {
		t.Fatalf("stale sample: %+v %v", result, err)
	}
	for name, bad := range map[string]metrics.Sample{
		"future":   sampleAt(5, now.Add(2*time.Minute), 1),
		"percent":  sampleAt(6, now, 101),
		"negative": {Seq: 7, CollectedAt: now.Unix(), NetRx: value(-1)},
		"nan":      {Seq: 8, CollectedAt: now.Unix(), Load1: value(math.NaN())},
		"seq":      sampleAt(0, now, 1),
	} {
		if _, err := s.RecordSamples(ctx, agent.serial, id, testRun, []metrics.Sample{sampleAt(9, now, 1), bad}, now); !errors.Is(err, ErrInput) {
			t.Fatalf("%s sample accepted: %v", name, err)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM metric_samples WHERE host_id=?", host.ID).Scan(&count); err != nil || count != 4 {
		t.Fatalf("stored %d samples: %v", count, err)
	}
	if err := s.RevokeHost(ctx, owner.Token, owner.CSRF, host.ID, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSamples(ctx, agent.serial, id, testRun, []metrics.Sample{sampleAt(10, now, 1)}, now); !errors.Is(err, ErrCredentials) {
		t.Fatalf("revoked Agent stored samples: %v", err)
	}
	if err := s.RecordHostInfo(ctx, agent.serial, id, metrics.HostInfo{CollectedAt: now.Unix()}, now); !errors.Is(err, ErrCredentials) {
		t.Fatalf("revoked Agent stored host information: %v", err)
	}
}

func TestHostDetailSeparatesCurrentFromLastKnownValues(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	agent := enrollAgent(t, s, owner, host, ca)
	id := agent.credential.AgentID
	now := testTime.Add(time.Minute)
	count := 4
	info := metrics.HostInfo{CollectedAt: now.Unix(), Hostname: "web-01", Kernel: "6.12.0", CPUCount: &count, Filesystems: []metrics.Filesystem{{Mount: "/", Type: "ext4"}}, Interfaces: []metrics.Interface{}}
	if err := s.RecordHostInfo(ctx, agent.serial, id, info, now); err != nil {
		t.Fatal(err)
	}
	older := info
	older.CollectedAt, older.Hostname = now.Add(-time.Minute).Unix(), "stale"
	if err := s.RecordHostInfo(ctx, agent.serial, id, older, now); err != nil {
		t.Fatal(err)
	}
	if bad := (metrics.HostInfo{CollectedAt: now.Unix(), Hostname: "bad\nname"}); s.RecordHostInfo(ctx, agent.serial, id, bad, now) == nil {
		t.Fatal("control characters accepted")
	}
	if _, err := s.RecordSamples(ctx, agent.serial, id, testRun, []metrics.Sample{sampleAt(1, now, 25)}, now); err != nil {
		t.Fatal(err)
	}
	session := loginAt(t, s, now)
	detail, err := s.HostDetail(ctx, session.Token, host.ID, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if detail.Info == nil || detail.Info.Hostname != "web-01" || detail.Info.ReceivedAt != now.Unix() {
		t.Fatalf("older information replaced newer: %+v", detail.Info)
	}
	if detail.Latest == nil || !detail.Latest.Current || *detail.Latest.CPU != 25 || detail.Latest.Swap != nil || detail.SampleInterval != 15 {
		t.Fatalf("latest: %+v", detail.Latest)
	}
	detail, err = s.HostDetail(ctx, session.Token, host.ID, now.Add(time.Minute))
	if err != nil || detail.Latest == nil || detail.Latest.Current {
		t.Fatalf("a minute-old value is still shown as current: %+v %v", detail.Latest, err)
	}

	other, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "other", testTime)
	if err != nil {
		t.Fatal(err)
	}
	_, viewer := inviteUser(t, s, owner, "viewer", rbac.Grant{Role: rbac.Viewer, HostID: other.ID})
	if _, err := s.HostDetail(ctx, viewer.Token, host.ID, testTime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("out-of-scope host detail: %v", err)
	}
	if _, err := s.MetricHistory(ctx, viewer.Token, host.ID, "1h", testTime); !errors.Is(err, ErrNotFound) {
		t.Fatalf("out-of-scope history: %v", err)
	}
	if _, err := s.HostDetail(ctx, viewer.Token, other.ID, testTime); err != nil {
		t.Fatalf("in-scope host detail: %v", err)
	}
	if err := s.DeleteHost(ctx, session.Token, session.CSRF, host.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HostDetail(ctx, session.Token, host.ID, now.Add(time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted host detail: %v", err)
	}
}

func TestHistoryAggregatesStepsAndLeavesGaps(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	agent := enrollAgent(t, s, owner, host, ca)
	now := testTime.Add(10 * time.Minute).Truncate(time.Hour).Add(time.Hour)
	// Two samples in one minute, then nothing for three minutes, then one.
	base := now.Add(-5 * time.Minute).Truncate(time.Minute)
	batch := []metrics.Sample{sampleAt(1, base, 10), sampleAt(2, base.Add(30*time.Second), 30), sampleAt(3, base.Add(4*time.Minute), 50)}
	batch[2].Memory = nil
	if _, err := s.RecordSamples(ctx, agent.serial, agent.credential.AgentID, testRun, batch, now); err != nil {
		t.Fatal(err)
	}
	session := loginAt(t, s, now)
	history, err := s.MetricHistory(ctx, session.Token, host.ID, "6h", now)
	if err != nil {
		t.Fatal(err)
	}
	if history.Step != 60 || len(history.Times) != 2 || history.Times[0] != base.Unix() || history.Times[1] != base.Add(4*time.Minute).Unix() {
		t.Fatalf("steps: %+v", history.Times)
	}
	cpu := history.Series["cpu"]
	if *cpu.Avg[0] != 20 || *cpu.Min[0] != 10 || *cpu.Max[0] != 30 || *cpu.Avg[1] != 50 {
		t.Fatalf("cpu: %v %v %v", *cpu.Avg[0], *cpu.Min[0], *cpu.Max[0])
	}
	if history.Series["memory"].Avg[1] != nil || history.Series["swap"].Avg[0] != nil {
		t.Fatal("a missing value was reported as a number")
	}
	if _, err := s.MetricHistory(ctx, session.Token, host.ID, "2h", now); !errors.Is(err, ErrInput) {
		t.Fatalf("unknown range: %v", err)
	}
}

// insertSample bypasses the acceptance window to build long histories.
func insertSample(t *testing.T, s *Store, agentID, hostID string, seq int64, at time.Time, cpu float64) {
	t.Helper()
	if _, err := s.db.Exec("INSERT INTO metric_samples(agent_id,run_id,seq,host_id,collected_at,received_at,cpu) VALUES(?,?,?,?,?,?,?)", agentID, testRun, seq, hostID, at.Unix(), at.Unix(), cpu); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionDeletesOnlyAggregatedSamples(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	agent := enrollAgent(t, s, owner, host, ca)
	start := testTime.Truncate(time.Hour)
	// One sample every 5 minutes for 9 days, alternating 10 and 30.
	seq := int64(0)
	for at := start; at.Before(start.Add(9 * 24 * time.Hour)); at = at.Add(5 * time.Minute) {
		seq++
		insertSample(t, s, agent.credential.AgentID, host.ID, seq, at, float64(10+20*(seq%2)))
		insertSample(t, s, agent.credential.AgentID, host.ID, seq+1<<40, at.Add(time.Minute), float64(10+20*((seq+1)%2)))
	}
	now := start.Add(9 * 24 * time.Hour)
	countRaw := func() (n int) {
		if err := s.db.QueryRow("SELECT count(*) FROM metric_samples WHERE collected_at<?", now.Add(-7*24*time.Hour).Unix()).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := countRaw()
	// One pass aggregates one day, so only that day may be deleted.
	if err := s.RunMonitoringMaintenance(ctx, now); err != nil {
		t.Fatal(err)
	}
	var rolled int64
	if err := s.db.QueryRow("SELECT rolled_up_until FROM monitoring_settings").Scan(&rolled); err != nil {
		t.Fatal(err)
	}
	if rolled != start.Add(24*time.Hour).Unix() {
		t.Fatalf("first pass rolled up to %d", rolled)
	}
	var unaggregated int
	if err := s.db.QueryRow("SELECT count(*) FROM metric_samples WHERE collected_at<?", rolled).Scan(&unaggregated); err != nil || unaggregated != 0 {
		t.Fatalf("aggregated raw rows kept past retention: %d %v", unaggregated, err)
	}
	if after := countRaw(); after != before-2*288 {
		t.Fatalf("deleted %d raw rows, want one day", before-after)
	}
	for range 12 {
		if err := s.RunMonitoringMaintenance(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	if countRaw() != 0 {
		t.Fatal("raw samples past retention remain")
	}
	// Repeating maintenance changes nothing.
	var rollups, again int
	if err := s.db.QueryRow("SELECT count(*) FROM metric_rollups").Scan(&rollups); err != nil {
		t.Fatal(err)
	}
	if err := s.RunMonitoringMaintenance(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM metric_rollups").Scan(&again); err != nil || again != rollups {
		t.Fatalf("maintenance is not idempotent: %d then %d", rollups, again)
	}

	session := loginAt(t, s, now)
	history, err := s.MetricHistory(ctx, session.Token, host.ID, "30d", now)
	if err != nil {
		t.Fatal(err)
	}
	if oldest := start.Unix() / 7200 * 7200; history.Times[0] != oldest {
		t.Fatalf("oldest step %d, want %d", history.Times[0], oldest)
	}
	cpu := history.Series["cpu"]
	for i := range history.Times[:len(history.Times)-1] {
		if *cpu.Avg[i] != 20 || *cpu.Min[i] != 10 || *cpu.Max[i] != 30 {
			t.Fatalf("step %d mixes sources wrongly: %v %v %v", i, *cpu.Avg[i], *cpu.Min[i], *cpu.Max[i])
		}
	}
	// Rollups past their retention are removed.
	later := now.Add(31 * 24 * time.Hour)
	if err := s.RunMonitoringMaintenance(ctx, later); err != nil {
		t.Fatal(err)
	}
	var old int
	if err := s.db.QueryRow("SELECT count(*) FROM metric_rollups WHERE bucket<?", later.Add(-30*24*time.Hour).Unix()).Scan(&old); err != nil || old != 0 {
		t.Fatalf("expired rollups kept: %d %v", old, err)
	}
}

func TestLateSamplesCannotEnterAggregatedBuckets(t *testing.T) {
	s, owner, host, ca := agentFixture(t)
	ctx := context.Background()
	agent := enrollAgent(t, s, owner, host, ca)
	now := testTime.Add(time.Hour)
	if _, err := s.RecordSamples(ctx, agent.serial, agent.credential.AgentID, testRun, []metrics.Sample{sampleAt(1, now.Add(-time.Minute), 10)}, now); err != nil {
		t.Fatal(err)
	}
	// Even inside the Agent's 10-minute window, a sample before the rollup
	// watermark would change a finished aggregate.
	if _, err := s.db.Exec("UPDATE monitoring_settings SET rolled_up_until=?", now.Unix()); err != nil {
		t.Fatal(err)
	}
	result, err := s.RecordSamples(ctx, agent.serial, agent.credential.AgentID, testRun, []metrics.Sample{sampleAt(2, now.Add(-2*time.Minute), 10)}, now)
	if err != nil || result.Expired != 1 {
		t.Fatalf("late sample: %+v %v", result, err)
	}
}
