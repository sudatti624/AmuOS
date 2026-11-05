package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/store"
)

func journalAgent(t *testing.T, root string, now time.Time) *Agent {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	return &Agent{cfg: Config{StateDir: root}, clock: func() time.Time { return now }}
}
func journalRecord(id string, at time.Time, ack bool, outcome string) localTask {
	return localTask{Delivery: store.TaskDelivery{ID: id, AttemptID: "attempt", Operation: "collect", InputHash: strings.Repeat("a", 64), RunDeadline: at.Unix()}, Outcome: outcome, Acknowledged: ack, TerminalAt: at.Unix(), AcknowledgedAt: at.Unix()}
}
func assertJournal(t *testing.T, a *Agent, id string, exists bool) {
	t.Helper()
	_, err := os.Stat(filepath.Join(a.path("tasks"), id+".json"))
	if exists && err != nil {
		t.Fatal(err)
	}
	if !exists && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record %s unexpectedly remains: %v", id, err)
	}
}

func TestJournalRetentionBoundariesAndRestart(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	root := t.TempDir()
	a := journalAgent(t, root, now)
	records := []localTask{
		journalRecord("old-success-00001", now.Add(-31*24*time.Hour), true, "succeeded"),
		journalRecord("old-failure-00001", now.Add(-31*24*time.Hour), true, "failed"),
		journalRecord("boundary-00000001", now.Add(-taskJournalRetention), true, "succeeded"),
		journalRecord("recent-000000001", now.Add(-time.Hour), true, "succeeded"),
		journalRecord("future-000000001", now.Add(365*24*time.Hour), true, "failed"),
		journalRecord("unacked-00000001", now.Add(-90*24*time.Hour), false, "succeeded"),
		journalRecord("unknown-00000001", now.Add(-90*24*time.Hour), false, "unknown"),
		journalRecord("acked-unknown-001", now.Add(-90*24*time.Hour), true, "unknown"),
	}
	for _, record := range records {
		if err := a.saveTask(record); err != nil {
			t.Fatal(err)
		}
	}
	late := journalRecord("late-server-unknown", now.Add(-90*24*time.Hour), true, "succeeded")
	late.ServerState = "unknown"
	if err := a.saveTask(late); err != nil {
		t.Fatal(err)
	}
	if err := a.scanTaskJournal(ctx); err != nil {
		t.Fatal(err)
	}
	for i, record := range records {
		assertJournal(t, a, record.Delivery.ID, i >= 2)
	}
	assertJournal(t, a, late.Delivery.ID, true)
	restarted := journalAgent(t, root, now.Add(2*time.Hour))
	if err := restarted.scanTaskJournal(ctx); err != nil {
		t.Fatal(err)
	}
	assertJournal(t, restarted, "boundary-00000001", false)
	for _, id := range []string{"unacked-00000001", "unknown-00000001", "acked-unknown-001", "future-000000001"} {
		assertJournal(t, restarted, id, true)
	}
}

func TestJournalLegacyTimestampSweepIntervalAndClockRollback(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	root := t.TempDir()
	a := journalAgent(t, root, now)
	legacy := journalRecord("legacy-000000001", now.Add(-90*24*time.Hour), true, "succeeded")
	legacy.TerminalAt = 0
	legacy.AcknowledgedAt = 0
	if err := a.saveTask(legacy); err != nil {
		t.Fatal(err)
	}
	if err := a.scanTaskJournal(ctx); err != nil {
		t.Fatal(err)
	}
	assertJournal(t, a, legacy.Delivery.ID, true)
	data, err := readPrivate(filepath.Join(a.path("tasks"), legacy.Delivery.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var migrated localTask
	if err := json.Unmarshal(data, &migrated); err != nil {
		t.Fatal(err)
	}
	if migrated.AcknowledgedAt != now.Unix() {
		t.Fatalf("unsafe legacy timestamp %d", migrated.AcknowledgedAt)
	}
	old := journalRecord("old-after-sweep-1", now.Add(-40*24*time.Hour), true, "failed")
	if err := a.saveTask(old); err != nil {
		t.Fatal(err)
	}
	a.clock = func() time.Time { return now.Add(30 * time.Minute) }
	if err := a.scanTaskJournal(ctx); err != nil {
		t.Fatal(err)
	}
	assertJournal(t, a, old.Delivery.ID, true)
	// This would be eligible by age, but the persisted high-water mark prevents
	// deletion after a backwards clock change even in a fresh Agent instance.
	backwards := journalAgent(t, root, now.Add(-24*time.Hour))
	if err := backwards.scanTaskJournal(ctx); err != nil {
		t.Fatal(err)
	}
	assertJournal(t, backwards, old.Delivery.ID, true)
	restarted := journalAgent(t, root, now.Add(2*time.Hour))
	if err := restarted.scanTaskJournal(ctx); err != nil {
		t.Fatal(err)
	}
	assertJournal(t, restarted, old.Delivery.ID, false)
	restarted = journalAgent(t, root, now.Add(taskJournalRetention+time.Second))
	if err := restarted.scanTaskJournal(ctx); err != nil {
		t.Fatal(err)
	}
	assertJournal(t, restarted, legacy.Delivery.ID, false)
}

func TestTaskJournalProblemsDoNotStopMonitoring(t *testing.T) {
	for _, kind := range []string{"full", "corrupt", "report-failed"} {
		t.Run(kind, func(t *testing.T) {
			cp := startControlPlane(t)
			ctx := context.Background()
			host, code := cp.code(t)
			directory := t.TempDir()
			a := cp.agent(t, directory)
			a.cfg.Collector = &fakeCollector{}
			if _, err := a.Enroll(ctx, code); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(a.path("tasks"), 0700); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			switch kind {
			case "full":
				// Legacy ACKs are deliberately stamped conservatively by the first sweep.
				for i := 0; i < taskJournalLimit; i++ {
					r := journalRecord(fmt.Sprintf("full-record-%08d", i), now, true, "succeeded")
					data, _ := json.Marshal(r)
					if err := os.WriteFile(filepath.Join(a.path("tasks"), r.Delivery.ID+".json"), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(a.path("tasks"), "broken.json"), []byte("{bad"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(a.path("tasks"), "unexpected.txt"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				task, err := cp.db.CreateTask(store.WithIdempotencyKey(ctx, "healthy-journal-task-0001"), cp.owner.Token, cp.owner.CSRF, host.ID, "collect", now)
				if err != nil {
					t.Fatal(err)
				}
				_, leaf, id, err := a.loadCredential()
				if err != nil {
					t.Fatal(err)
				}
				delivery, err := cp.db.PollTasks(ctx, agentca.SerialString(leaf.SerialNumber), id.AgentID, nil, now)
				if err != nil {
					t.Fatal(err)
				}
				receipt := store.TaskReceipt{ID: task.ID, AttemptID: delivery.AttemptID, InputHash: delivery.InputHash}
				if _, err := cp.db.PollTasks(ctx, agentca.SerialString(leaf.SerialNumber), id.AgentID, &receipt, now); err != nil {
					t.Fatal(err)
				}
				if err := a.saveTask(localTask{Delivery: *delivery, Outcome: "failed"}); err != nil {
					t.Fatal(err)
				}
			case "report-failed":
				if err := a.saveTask(journalRecord("absent-server-0001", now, false, "failed")); err != nil {
					t.Fatal(err)
				}
			}
			for cycle := 0; cycle < 2; cycle++ {
				if cycle == 1 {
					a = cp.agent(t, directory)
					a.cfg.Collector = &fakeCollector{}
				}
				a.nextInfo = time.Time{}
				a.collect(time.Now())
				if err := a.sync(ctx); err != nil {
					t.Fatal(err)
				}
				detail, err := cp.db.HostDetail(ctx, cp.owner.Token, host.ID, time.Now())
				if err != nil || detail.Latest == nil || detail.Info == nil || detail.Info.Hostname != "managed-01" || len(a.pending) != 0 {
					t.Fatalf("monitoring stopped: %+v %v", detail, err)
				}
				if kind == "full" {
					if !a.tasksBlocked || slices.Contains(a.capabilities(), "tasks") {
						t.Fatal("full journal still advertises tasks")
					}
					if err := a.Beat(ctx); err != nil {
						t.Fatal(err)
					}
					detail, err = cp.db.HostDetail(ctx, cp.owner.Token, host.ID, time.Now())
					if err != nil || slices.Contains(detail.Agent.Capabilities, "tasks") {
						t.Fatal("heartbeat failed to advertise full journal")
					}
				}
			}
			if kind == "corrupt" {
				tasks, err := cp.db.Tasks(ctx, cp.owner.Token, time.Now())
				if err != nil || len(tasks) != 1 || tasks[0].State != "failed" {
					t.Fatalf("healthy result was blocked by corrupt record: %+v %v", tasks, err)
				}
				data, err := os.ReadFile(filepath.Join(a.path("tasks"), "broken.json"))
				if err != nil || string(data) != "{bad" {
					t.Fatal("corrupt record was removed")
				}
			}
		})
	}
}

func TestExpiredDeliveryAfterJournalDeletionDoesNotCollect(t *testing.T) {
	cp := startControlPlane(t)
	ctx := context.Background()
	host, code := cp.code(t)
	a := cp.agent(t, t.TempDir())
	a.cfg.Collector = &fakeCollector{}
	if _, err := a.Enroll(ctx, code); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	task, err := cp.db.CreateTask(store.WithIdempotencyKey(ctx, "expired-delivery-test-1"), cp.owner.Token, cp.owner.CSRF, host.ID, "collect", now)
	if err != nil {
		t.Fatal(err)
	}
	_, leaf, id, err := a.loadCredential()
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := cp.db.PollTasks(ctx, agentca.SerialString(leaf.SerialNumber), id.AgentID, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if stale, err := cp.db.PollTasks(ctx, agentca.SerialString(leaf.SerialNumber), id.AgentID, nil, now.Add(time.Minute)); err != nil || stale != nil {
		t.Fatalf("server delivered expired task: %v %v", stale, err)
	}
	if err := os.MkdirAll(a.path("tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	future := now.Add(32 * 24 * time.Hour)
	a.clock = func() time.Time { return future }
	if err := a.saveTask(localTask{Delivery: *delivery, Outcome: "succeeded", Acknowledged: true, TerminalAt: now.Unix(), AcknowledgedAt: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := a.scanTaskJournal(ctx); err != nil {
		t.Fatal(err)
	}
	assertJournal(t, a, task.ID, false)
	// Simulate a delayed response containing that same expired delivery. The
	// mTLS test listener uses the same trusted server and Agent CA boundaries.
	cert, roots := serverCertificate(t)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/agent/v1/tasks/poll":
			_ = json.NewEncoder(w).Encode(map[string]any{"task": delivery})
		case "/agent/v1/tasks/" + task.ID + "/result":
			_ = json.NewEncoder(w).Encode(map[string]bool{"acknowledged": true})
		default:
			w.WriteHeader(204)
		}
	}))
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: cp.ca.Pool()}
	proxy.StartTLS()
	defer proxy.Close()
	a.cfg.AgentServer = proxy.URL
	a.cfg.Roots = roots
	a.supported = true
	if err := a.syncTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if a.seq != 0 {
		t.Fatal("expired task was reexecuted")
	}
}

func TestRejectedTaskCredentialStillStopsAgent(t *testing.T) {
	cp := startControlPlane(t)
	ctx := context.Background()
	host, code := cp.code(t)
	a := cp.agent(t, t.TempDir())
	if _, err := a.Enroll(ctx, code); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.path("tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.saveTask(journalRecord("unreported-000001", time.Now(), false, "unknown")); err != nil {
		t.Fatal(err)
	}
	if err := cp.db.RevokeHost(ctx, cp.owner.Token, cp.owner.CSRF, host.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := a.syncTasksSafely(ctx); !errors.Is(err, ErrRejected) {
		t.Fatalf("credential rejection swallowed: %v", err)
	}
}

func TestServerUnknownReceiptIsNeverPurgedAsLocalSuccess(t *testing.T) {
	cp := startControlPlane(t)
	ctx := context.Background()
	host, code := cp.code(t)
	a := cp.agent(t, t.TempDir())
	if _, err := a.Enroll(ctx, code); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	task, err := cp.db.CreateTask(store.WithIdempotencyKey(ctx, "late-result-journal-01"), cp.owner.Token, cp.owner.CSRF, host.ID, "collect", now)
	if err != nil {
		t.Fatal(err)
	}
	_, leaf, id, err := a.loadCredential()
	if err != nil {
		t.Fatal(err)
	}
	d, err := cp.db.PollTasks(ctx, agentca.SerialString(leaf.SerialNumber), id.AgentID, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	receipt := store.TaskReceipt{ID: task.ID, AttemptID: d.AttemptID, InputHash: d.InputHash}
	if _, err := cp.db.PollTasks(ctx, agentca.SerialString(leaf.SerialNumber), id.AgentID, &receipt, now); err != nil {
		t.Fatal(err)
	}
	if err := cp.db.RecordTaskResult(ctx, agentca.SerialString(leaf.SerialNumber), id.AgentID, store.TaskResult{TaskReceipt: receipt, Outcome: "succeeded"}, now.Add(31*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.path("tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	local := localTask{Delivery: *d, Outcome: "succeeded"}
	if err := a.reportTask(ctx, &local); err != nil {
		t.Fatal(err)
	}
	if !local.Acknowledged || local.ServerState != "unknown" {
		t.Fatalf("receipt %+v", local)
	}
	restarted := journalAgent(t, a.cfg.StateDir, now.Add(40*24*time.Hour))
	if err := restarted.scanTaskJournal(ctx); err != nil {
		t.Fatal(err)
	}
	assertJournal(t, restarted, task.ID, true)
}
