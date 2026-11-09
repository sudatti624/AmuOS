package recovery

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/metrics"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/store"
)

type gateAgent struct {
	host       store.Host
	credential store.AgentCredential
	serial     string
}

func gateEnroll(t *testing.T, db *store.Store, owner store.Session, host store.Host, ca *agentca.CA, now time.Time) gateAgent {
	t.Helper()
	code, err := db.CreateEnrollment(t.Context(), owner.Token, owner.CSRF, host.ID, 10*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := db.Enroll(t.Context(), code.Code, csr, ca, now)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(credential.Certificate))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return gateAgent{host, credential, agentca.SerialString(cert.SerialNumber)}
}

// Fixed work, bounded deadline, and injected collection times make this gate
// repeatable in CI. It is not a claim of 24 hours of real-time operation.
func TestFoundationLoadAndIndependentRestore(t *testing.T) {
	started := time.Now()
	f := prepared(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	db, err := store.Open(f.database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	now := time.Now().Truncate(time.Second)
	ca, err := agentca.Load(filepath.Dir(f.database), now)
	if err != nil {
		t.Fatal(err)
	}
	agents := make([]gateAgent, 10)
	for i := range agents {
		host := f.host
		if i != 0 {
			host, err = db.CreateHost(ctx, f.session.Token, f.session.CSRF, fmt.Sprintf("load-%02d", i), now)
			if err != nil {
				t.Fatal(err)
			}
		}
		agents[i] = gateEnroll(t, db, f.session, host, ca, now)
	}
	users := make([]store.Session, 5)
	for i := range users {
		name := fmt.Sprintf("load-user-%d", i)
		inv, err := db.CreateInvitation(ctx, f.session.Token, f.session.CSRF, name, []rbac.Grant{{Role: rbac.Operator, HostID: agents[i*2].host.ID}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.AcceptInvitation(ctx, inv.Token, password, now); err != nil {
			t.Fatal(err)
		}
		users[i], err = db.Login(ctx, name, password, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var wg sync.WaitGroup
	errorsCh := make(chan error, 15)
	start := make(chan struct{})
	// 240 samples/host, 15 second spacing, one synthetic hour; resend each
	// batch once to simulate a lost acknowledgement without missing samples.
	for _, a := range agents {
		wg.Go(func() {
			<-start
			for batch := 0; batch < 12; batch++ {
				at := now.Add(time.Duration(batch*20) * 15 * time.Second)
				samples := make([]metrics.Sample, 20)
				for j := range samples {
					v := float64(j + 1)
					samples[j] = metrics.Sample{Seq: int64(batch*20 + j + 1), CollectedAt: at.Add(time.Duration(j) * 15 * time.Second).Unix(), CPU: &v}
				}
				at = at.Add(19 * 15 * time.Second)
				for replay := 0; replay < 2; replay++ {
					r, err := db.RecordSamples(ctx, a.serial, a.credential.AgentID, "foundation-load-run", samples, at)
					if err != nil || (replay == 0 && r.Accepted != 20) || (replay == 1 && r.Duplicates != 20) {
						errorsCh <- fmt.Errorf("samples batch %d replay %d: %+v %v", batch, replay, r, err)
						return
					}
				}
			}
		})
	}
	for i, user := range users {
		wg.Go(func() {
			<-start
			a := agents[i*2]
			for n := 0; n < 20; n++ {
				if _, err := db.HostDetail(ctx, user.Token, a.host.ID, now); err != nil {
					errorsCh <- err
					return
				}
				if _, err := db.MetricHistory(ctx, user.Token, a.host.ID, "1h", now); err != nil {
					errorsCh <- err
					return
				}
				if _, err := db.HostDetail(ctx, user.Token, agents[i*2+1].host.ID, now); !errors.Is(err, store.ErrNotFound) {
					errorsCh <- fmt.Errorf("scope escaped: %v", err)
					return
				}
				key := fmt.Sprintf("foundation-task-%d-%04d", i, n)
				task, err := db.CreateTask(store.WithIdempotencyKey(ctx, key), user.Token, user.CSRF, a.host.ID, "collect", now)
				if err != nil {
					errorsCh <- err
					return
				}
				again, err := db.CreateTask(store.WithIdempotencyKey(ctx, key), user.Token, user.CSRF, a.host.ID, "collect", now)
				if err != nil || again.ID != task.ID {
					errorsCh <- fmt.Errorf("task replay: %v", err)
					return
				}
				delivery, err := db.PollTasks(ctx, a.serial, a.credential.AgentID, nil, now)
				if err != nil || delivery == nil || delivery.ID != task.ID {
					errorsCh <- fmt.Errorf("delivery: %v", err)
					return
				}
				receipt := store.TaskReceipt{ID: delivery.ID, AttemptID: delivery.AttemptID, InputHash: delivery.InputHash}
				if _, err := db.PollTasks(ctx, a.serial, a.credential.AgentID, &receipt, now); err != nil {
					errorsCh <- err
					return
				}
				result := store.TaskResult{TaskReceipt: receipt, Outcome: "succeeded"}
				for range 2 {
					if err := db.RecordTaskResult(ctx, a.serial, a.credential.AgentID, result, now); err != nil {
						errorsCh <- err
						return
					}
				}
			}
		})
	}
	close(start)
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	at := now.Add(time.Hour)
	f.session, err = db.Login(ctx, "owner", password, at)
	if err != nil {
		t.Fatal(err)
	}
	// Keep all five sessions valid at the snapshot boundary, so post-restore
	// rejection proves revocation rather than the ordinary idle timeout.
	for i := range users {
		users[i], err = db.Login(ctx, fmt.Sprintf("load-user-%d", i), password, at)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Identity(ctx, users[i].Token, at); err != nil {
			t.Fatal(err)
		}
	}
	histories := make([]store.History, 10)
	for i, a := range agents {
		histories[i], err = db.MetricHistory(ctx, f.session.Token, a.host.ID, "1h", at)
		if err != nil || len(histories[i].Times) == 0 {
			t.Fatalf("history %d: %v", i, err)
		}
	}
	counts, err := db.BackupCounts(ctx)
	if err != nil || counts["metric_samples"] != 2400 || counts["tasks"] != 100 || counts["task_attempts"] != 100 {
		t.Fatalf("load counts: %v %v", counts, err)
	}
	tasks, err := db.Tasks(ctx, f.session.Token, at)
	if err != nil || len(tasks) != 100 {
		t.Fatalf("completed tasks: %d %v", len(tasks), err)
	}
	for _, task := range tasks {
		if task.State != "succeeded" {
			t.Fatalf("unfinished task: %s", task.State)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "load.age")
	backupStart := time.Now()
	m, err := Backup(ctx, f.database, archive, f.identity.Recipient().String(), "test-version", testConfig)
	if err != nil {
		t.Fatal(err)
	}
	backupTime := time.Since(backupStart)
	if backupTime > 15*time.Minute {
		t.Fatal("backup exceeded 15 minute target")
	}
	// A new directory receives only the encrypted archive and offline key.
	target := filepath.Join(t.TempDir(), "clean", "amuos.db")
	restoreStart := time.Now()
	if _, err := Restore(ctx, target, archive, f.keyFile, "test-version"); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range append(users, f.session) {
		if _, err := db.Identity(ctx, user.Token, at); !errors.Is(err, store.ErrCredentials) {
			t.Fatalf("old session accepted: %v", err)
		}
	}
	owner, err := db.Login(ctx, "owner", password, at)
	if err != nil {
		t.Fatal(err)
	}
	restoredTasks, err := db.Tasks(ctx, owner.Token, at)
	if err != nil || !reflect.DeepEqual(tasks, restoredTasks) {
		t.Fatalf("restored task records differ: %v", err)
	}
	for i, a := range agents {
		got, err := db.MetricHistory(ctx, owner.Token, a.host.ID, "1h", at)
		if err != nil || !reflect.DeepEqual(got, histories[i]) {
			t.Fatalf("restored history %d differs: %v", i, err)
		}
		if _, err := db.RecordSamples(ctx, a.serial, a.credential.AgentID, "foundation-load-run", []metrics.Sample{{Seq: 241, CollectedAt: at.Unix()}}, at); !errors.Is(err, store.ErrCredentials) {
			t.Fatalf("old agent accepted: %v", err)
		}
	}
	for i := range users {
		user, err := db.Login(ctx, fmt.Sprintf("load-user-%d", i), password, at)
		if err != nil {
			t.Fatal(err)
		}
		hosts, err := db.Hosts(ctx, user.Token, at)
		if err != nil || len(hosts) != 1 || hosts[0].ID != agents[i*2].host.ID {
			t.Fatalf("restored scope %d: %+v %v", i, hosts, err)
		}
	}
	restored, err := db.BackupCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"users", "grants", "hosts", "metric_samples", "metric_rollups", "tasks", "task_attempts"} {
		if restored[table] != m.Counts[table] {
			t.Fatalf("restored %s: %d want %d", table, restored[table], m.Counts[table])
		}
	}
	newCA, err := agentca.Load(filepath.Dir(target), at)
	if err != nil {
		t.Fatal(err)
	}
	a := gateEnroll(t, db, owner, agents[0].host, newCA, at)
	if _, err := db.RecordSamples(ctx, a.serial, a.credential.AgentID, "foundation-resumed", []metrics.Sample{{Seq: 1, CollectedAt: at.Unix()}}, at); err != nil {
		t.Fatal(err)
	}
	if time.Since(restoreStart) > 60*time.Minute {
		t.Fatal("monitoring recovery exceeded 60 minute target")
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("hosts=10 users=5 samples=2400 tasks=100; elapsed=%s backup=%s restore-to-sample=%s db-bytes=%d heap-before=%d heap-after=%d", time.Since(started), backupTime, time.Since(restoreStart), info.Size(), before.HeapAlloc, after.HeapAlloc)
}
