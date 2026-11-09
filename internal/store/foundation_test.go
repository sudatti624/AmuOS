package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Seed the declared raw retention volume with SQL; test maintenance through
// the production API. This models data volume, not elapsed wall-clock time.
func TestFoundationRetentionVolume(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	started := time.Now()
	s, owner, host, ca := agentFixture(t)
	start := testTime.Truncate(24 * time.Hour)
	for i := 0; i < 10; i++ {
		if i != 0 {
			var err error
			host, err = s.CreateHost(ctx, owner.Token, owner.CSRF, fmt.Sprintf("retention-%d", i), testTime)
			if err != nil {
				t.Fatal(err)
			}
		}
		a := enrollAgent(t, s, owner, host, ca)
		// Seven days at fifteen seconds per host (403,200 raw rows total).
		_, err := s.db.ExecContext(ctx, `WITH RECURSIVE ticks(n) AS (VALUES(0) UNION ALL SELECT n+1 FROM ticks WHERE n<40319)
		INSERT INTO metric_samples(agent_id,run_id,seq,host_id,collected_at,received_at,cpu,memory)
		SELECT ?, 'foundation-volume', n+1, ?, ?+n*15, ?+n*15, 25, 50 FROM ticks`, a.credential.AgentID, host.ID, start.Unix(), start.Unix())
		if err != nil {
			t.Fatal(err)
		}
	}
	readCount := func(query string) int64 {
		t.Helper()
		var n int64
		if err := s.db.QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := readCount("SELECT count(*) FROM metric_samples"); n != 403200 {
		t.Fatalf("raw rows %d", n)
	}
	pagesBefore := readCount("PRAGMA page_count")
	now := start.Add(8 * 24 * time.Hour)
	for range 9 {
		if err := s.RunMonitoringMaintenance(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	// Exactly the first day is beyond raw retention; no unaggregated loss.
	if n := readCount("SELECT count(*) FROM metric_samples"); n != 345600 {
		t.Fatalf("retained raw rows %d", n)
	}
	if n := readCount("SELECT sum(count) FROM metric_rollups WHERE metric='cpu'"); n != 403200 {
		t.Fatalf("aggregated CPU samples %d", n)
	}
	if n := readCount("SELECT count(*) FROM metric_rollups"); n != 40320 {
		t.Fatalf("rollup rows %d", n)
	}
	pagesPeak := readCount("PRAGMA page_count")
	if err := s.RunMonitoringMaintenance(ctx, now); err != nil {
		t.Fatal(err)
	}
	if n := readCount("PRAGMA page_count"); n != pagesPeak {
		t.Fatalf("idempotent maintenance grew DB: %d -> %d", pagesPeak, n)
	}
	if err := s.RunMonitoringMaintenance(ctx, now.Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := readCount("SELECT count(*) FROM metric_samples"); n != 0 {
		t.Fatalf("expired raw rows %d", n)
	}
	if n := readCount("SELECT count(*) FROM metric_rollups"); n != 0 {
		t.Fatalf("expired rollups %d", n)
	}
	if n := readCount("PRAGMA freelist_count"); n == 0 {
		t.Fatal("expired data pages were not released for reuse")
	}
	if n := readCount("PRAGMA page_count"); n > pagesPeak {
		t.Fatalf("purge grew DB: %d -> %d", pagesPeak, n)
	}
	t.Logf("synthetic hosts=10 raw-days=7 interval=15s rows=403200 rollups=40320; pages-before=%d pages-peak=%d elapsed=%s", pagesBefore, pagesPeak, time.Since(started))
}
