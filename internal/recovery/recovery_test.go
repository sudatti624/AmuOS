package recovery

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/statelock"
	"github.com/sudatti624/AmuOS/internal/store"
)

const password = "correct horse battery staple"

var testConfig = Config{FormatVersion: 1, Address: "127.0.0.1:8443", Origin: "https://localhost:8443"}

type fixture struct {
	database, archive, keyFile string
	identity                   *age.X25519Identity
	session                    store.Session
	invitation                 store.Invitation
	host                       store.Host
	manifest                   Manifest
}

func prepared(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	f := fixture{database: filepath.Join(root, "state", "amuos.db"), archive: filepath.Join(root, "backup.age"), keyFile: filepath.Join(root, "recovery.key")}
	var err error
	f.identity, err = age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.keyFile, []byte("# offline recovery key\n"+f.identity.String()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(f.database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	setup, err := db.PrepareBootstrap(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Setup(ctx, setup, "owner", password, now); err != nil {
		t.Fatal(err)
	}
	f.session, err = db.Login(ctx, "owner", password, now)
	if err != nil {
		t.Fatal(err)
	}
	f.host, err = db.CreateHost(ctx, f.session.Token, f.session.CSRF, "host-a", now)
	if err != nil {
		t.Fatal(err)
	}
	f.invitation, err = db.CreateInvitation(ctx, f.session.Token, f.session.CSRF, "viewer", []rbac.Grant{{Role: rbac.Viewer, HostID: f.host.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f.manifest, err = Backup(ctx, f.database, f.archive, f.identity.Recipient().String(), "test-version", testConfig)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func count(t *testing.T, database, query string) int {
	t.Helper()
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRoundTripPreservesDataAndRevokesCredentials(t *testing.T) {
	f := prepared(t)
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "restored", "amuos.db")
	// An Agent CA already in the target directory must not survive the restore.
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := agentca.Load(filepath.Dir(target), time.Now()); err != nil {
		t.Fatal(err)
	}
	result, err := Restore(ctx, target, f.archive, f.keyFile, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if result.Backup.ID != f.manifest.ID || result.Config != testConfig || result.RetainedDatabase != "" {
		t.Fatalf("unexpected restore result: %+v", result)
	}
	for _, name := range []string{agentca.CertFile, agentca.KeyFile} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(target), name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("agent CA file %s survived the restore: %v", name, err)
		}
	}
	if err := store.ValidateSnapshot(ctx, target); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Identity(ctx, f.session.Token, time.Now()); !errors.Is(err, store.ErrCredentials) {
		t.Fatalf("old session survived: %v", err)
	}
	if _, err := db.AcceptInvitation(ctx, f.invitation.Token, password, time.Now()); err == nil {
		t.Fatal("old invitation survived")
	}
	session, err := db.Login(ctx, "owner", password, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := db.Hosts(ctx, session.Token, time.Now())
	if err != nil || len(hosts) != 1 || hosts[0].ID != f.host.ID {
		t.Fatalf("hosts: %v %v", hosts, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := count(t, target, "SELECT count(*) FROM local_maintenance WHERE operation='backup' AND state='unknown'"); got != 1 {
		t.Fatalf("interrupted backup state=%d", got)
	}
	if got := count(t, target, "SELECT count(*) FROM audit_events WHERE action='restore.complete'"); got != 1 {
		t.Fatalf("restore audit=%d", got)
	}
	if got := count(t, f.database, "SELECT count(*) FROM local_maintenance WHERE operation='backup' AND state='succeeded'"); got != 1 {
		t.Fatalf("source backup state=%d", got)
	}
	// The new state remains usable after closing and reopening it.
	db, err = store.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Identity(ctx, session.Token, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Identity(ctx, f.session.Token, time.Now()); !errors.Is(err, store.ErrCredentials) {
		t.Fatal("old credential revived after restart")
	}
}

func TestRestoreRetainsConsistentExistingDatabase(t *testing.T) {
	f := prepared(t)
	db, err := store.Open(f.database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateHost(context.Background(), f.session.Token, f.session.CSRF, "after-backup", time.Now()); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	result, err := Restore(context.Background(), f.database, f.archive, f.keyFile, "test-version")
	if err != nil {
		t.Fatal(err)
	}
	if result.RetainedDatabase == "" {
		t.Fatal("missing pre-restore snapshot")
	}
	if got := count(t, result.RetainedDatabase, "SELECT count(*) FROM hosts"); got != 2 {
		t.Fatalf("retained hosts=%d", got)
	}
	if got := count(t, f.database, "SELECT count(*) FROM hosts"); got != 1 {
		t.Fatalf("restored hosts=%d", got)
	}
	if err := store.ValidateSnapshot(context.Background(), result.RetainedDatabase); err != nil {
		t.Fatal(err)
	}
}

type entry struct {
	name string
	data []byte
	kind byte
	size int64
}

func contents(t *testing.T, f fixture) (Manifest, []entry) {
	t.Helper()
	file, err := os.Open(f.archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	plain, err := age.Decrypt(file, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(plain)
	var m Manifest
	var entries []entry
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "manifest.json" {
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
		} else {
			entries = append(entries, entry{name: h.Name, data: data, kind: tar.TypeReg, size: int64(len(data))})
		}
	}
	return m, entries
}

func crafted(t *testing.T, recipient age.Recipient, manifest Manifest, entries []entry, trailing []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	encrypted, err := age.Encrypt(&out, recipient)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(encrypted)
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	all := append([]entry{{name: "manifest.json", data: data, kind: tar.TypeReg, size: int64(len(data))}}, entries...)
	for _, e := range all {
		header := &tar.Header{Name: e.name, Size: e.size, Mode: 0600, Typeflag: e.kind, Format: tar.FormatUSTAR}
		if e.kind == tar.TypeSymlink {
			header.Linkname = "../outside"
			header.Size = 0
		}
		if err := w.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if e.kind == tar.TypeReg {
			if _, err := w.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := encrypted.Write(trailing); err != nil {
		t.Fatal(err)
	}
	if err := encrypted.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestInvalidArchivesNeverReplaceExistingState(t *testing.T) {
	f := prepared(t)
	original, err := os.ReadFile(f.database)
	if err != nil {
		t.Fatal(err)
	}
	validCiphertext, err := os.ReadFile(f.archive)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		change    func(*Manifest, *[]entry)
		transform func([]byte) []byte
		version   string
		wrongKey  bool
		tail      []byte
	}{
		{name: "wrong-key", wrongKey: true},
		{name: "wrong-product", version: "another-version"},
		{name: "cut-final-chunk", transform: func(b []byte) []byte { return b[:len(b)-1] }},
		{name: "tampered", transform: func(b []byte) []byte { b[len(b)-1] ^= 1; return b }},
		{name: "future-format", change: func(m *Manifest, _ *[]entry) { m.FormatVersion++ }},
		{name: "future-schema", change: func(m *Manifest, _ *[]entry) { m.SchemaVersion++ }},
		{name: "wrong-hash", change: func(m *Manifest, _ *[]entry) {
			r := m.Files["database.sqlite"]
			r.SHA256 = strings.Repeat("0", 64)
			m.Files["database.sqlite"] = r
		}},
		{name: "wrong-record-count", change: func(m *Manifest, _ *[]entry) { m.Counts["users"]++ }},
		{name: "missing-file", change: func(_ *Manifest, e *[]entry) { *e = (*e)[:2] }},
		{name: "duplicate-file", change: func(_ *Manifest, e *[]entry) { *e = append(*e, (*e)[0]) }},
		{name: "parent-path", change: func(_ *Manifest, e *[]entry) { (*e)[0].name = "../database.sqlite" }},
		{name: "absolute-path", change: func(_ *Manifest, e *[]entry) { (*e)[0].name = "/database.sqlite" }},
		{name: "symlink", change: func(_ *Manifest, e *[]entry) { (*e)[0].kind = tar.TypeSymlink }},
		{name: "oversized-declaration", change: func(m *Manifest, _ *[]entry) {
			r := m.Files["database.sqlite"]
			r.Size = maxDatabase + 1
			m.Files["database.sqlite"] = r
		}},
		{name: "extra-entry", change: func(_ *Manifest, e *[]entry) { *e = append(*e, entry{name: "unlisted", kind: tar.TypeReg}) }},
		{name: "trailing-data", tail: []byte("unexpected")},
		{name: "corrupt-sqlite", change: func(m *Manifest, e *[]entry) {
			(*e)[0].data = []byte("not a database")
			(*e)[0].size = int64(len((*e)[0].data))
			m.Files["database.sqlite"] = record((*e)[0].data)
		}},
		{name: "nonempty-key-bundle", change: func(m *Manifest, e *[]entry) {
			for i := range *e {
				if (*e)[i].name == "keys.json" {
					(*e)[i].data = []byte(`{"format_version":1,"keys":[{}]}`)
					(*e)[i].size = int64(len((*e)[i].data))
					m.Files["keys.json"] = record((*e)[i].data)
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, entries := contents(t, f)
			if test.change != nil {
				test.change(&m, &entries)
			}
			ciphertext := validCiphertext
			if test.change != nil || test.tail != nil {
				ciphertext = crafted(t, f.identity.Recipient(), m, entries, test.tail)
			}
			if test.transform != nil {
				ciphertext = test.transform(append([]byte{}, ciphertext...))
			}
			path := filepath.Join(t.TempDir(), "input.age")
			if err := os.WriteFile(path, ciphertext, 0600); err != nil {
				t.Fatal(err)
			}
			keyFile := f.keyFile
			if test.wrongKey {
				key, _ := age.GenerateX25519Identity()
				keyFile = filepath.Join(t.TempDir(), "wrong.key")
				if err := os.WriteFile(keyFile, []byte(key.String()), 0600); err != nil {
					t.Fatal(err)
				}
			}
			version := "test-version"
			if test.version != "" {
				version = test.version
			}
			if _, err := Restore(context.Background(), f.database, path, keyFile, version); err == nil {
				t.Fatal("accepted invalid archive")
			}
			after, err := os.ReadFile(f.database)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, after) {
				t.Fatal("invalid archive modified current DB")
			}
			staging, err := filepath.Glob(filepath.Join(filepath.Dir(f.database), ".restore-stage-*"))
			if err != nil || len(staging) != 0 {
				t.Fatalf("staging leak: %v %v", staging, err)
			}
		})
	}
}

func TestLockCancellationAndOverwriteProtection(t *testing.T) {
	f := prepared(t)
	ctx := context.Background()
	before, err := os.ReadFile(f.archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(ctx, f.database, f.archive, f.identity.Recipient().String(), "test-version", testConfig); err == nil {
		t.Fatal("overwrote existing backup")
	}
	after, _ := os.ReadFile(f.archive)
	if !bytes.Equal(before, after) {
		t.Fatal("backup changed")
	}
	lock, err := statelock.Acquire(f.database)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	out := filepath.Join(t.TempDir(), "new.age")
	if _, err := Backup(ctx, f.database, out, f.identity.Recipient().String(), "test-version", testConfig); !errors.Is(err, statelock.ErrLocked) {
		t.Fatalf("locked backup: %v", err)
	}
	if _, err := Restore(ctx, f.database, f.archive, f.keyFile, "test-version"); !errors.Is(err, statelock.ErrLocked) {
		t.Fatalf("locked restore: %v", err)
	}
	_ = lock.Close()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Backup(canceled, f.database, out, f.identity.Recipient().String(), "test-version", testConfig); err == nil {
		t.Fatal("backup ignored cancellation")
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled backup published")
	}
	if _, err := Restore(canceled, f.database, f.archive, f.keyFile, "test-version"); err == nil {
		t.Fatal("restore ignored cancellation")
	}
	// A missing output directory simulates an output I/O failure after capture.
	if _, err := Backup(ctx, f.database, filepath.Join(t.TempDir(), "missing", "out.age"), f.identity.Recipient().String(), "test-version", testConfig); err == nil {
		t.Fatal("backup ignored output error")
	}
	if n := count(t, f.database, "SELECT count(*) FROM local_maintenance WHERE state='failed'"); n != 1 {
		t.Fatalf("failed backup records=%d", n)
	}
	stages, _ := filepath.Glob(filepath.Join(filepath.Dir(f.database), ".backup-stage-*"))
	if len(stages) != 0 {
		t.Fatalf("backup staging leak: %v", stages)
	}
}

func TestMaintenanceCrashHelper(t *testing.T) {
	database := os.Getenv("AMUOS_MAINTENANCE_CRASH_DATABASE")
	if database == "" {
		return
	}
	lock, err := statelock.Acquire(database)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	db, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.StartLocalMaintenance(context.Background(), "crash-id", "backup", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	directory, cleanup, err := scratch(filepath.Dir(database), ".backup-stage-")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := os.WriteFile(filepath.Join(directory, "database.sqlite"), []byte("unfinished private snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString("ready\n")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestCrashRecoveryRemovesOnlyMarkedStaging(t *testing.T) {
	f := prepared(t)
	child := exec.Command(os.Args[0], "-test.run=^TestMaintenanceCrashHelper$")
	child.Env = append(os.Environ(), "AMUOS_MAINTENANCE_CRASH_DATABASE="+f.database)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("child startup: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("crash helper timeout")
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	unmarked := filepath.Join(filepath.Dir(f.database), ".backup-stage-user-data")
	if err := os.Mkdir(unmarked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unmarked, "keep.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "after-crash.age")
	if _, err := Backup(context.Background(), f.database, out, f.identity.Recipient().String(), "test-version", testConfig); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.database, "SELECT count(*) FROM local_maintenance WHERE id='crash-id' AND state='unknown'"); n != 1 {
		t.Fatalf("unknown records=%d", n)
	}
	stages, _ := filepath.Glob(filepath.Join(filepath.Dir(f.database), ".backup-stage-*"))
	if len(stages) != 1 || stages[0] != unmarked {
		t.Fatalf("unexpected stages: %v", stages)
	}
	if _, err := os.Stat(filepath.Join(unmarked, "keep.txt")); err != nil {
		t.Fatal("deleted unrelated file")
	}
}

func TestShortWriteAndUnsafeStageAreRejected(t *testing.T) {
	f := prepared(t)
	m, entries := contents(t, f)
	var config, keys []byte
	var database []byte
	for _, e := range entries {
		switch e.name {
		case "database.sqlite":
			database = e.data
		case "config.json":
			config = e.data
		case "keys.json":
			keys = e.data
		}
	}
	source := filepath.Join(t.TempDir(), "db.sqlite")
	if err := os.WriteFile(source, database, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeArchive(context.Background(), &shortWriter{remaining: 1000}, f.identity.Recipient(), f.identity.Recipient(), m, source, config, keys); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}
	directory, _, err := scratch(filepath.Dir(f.database), ".restore-stage-")
	if err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(directory, "do-not-delete")
	if err := os.WriteFile(unexpected, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStages(f.database); err == nil {
		t.Fatal("accepted unsafe staging contents")
	}
	if _, err := os.Stat(unexpected); err != nil {
		t.Fatal("deleted unrecognized staging contents")
	}
}

type shortWriter struct{ remaining int }

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		n := w.remaining
		w.remaining = 0
		return n, io.ErrShortWrite
	}
	w.remaining -= len(p)
	return len(p), nil
}
