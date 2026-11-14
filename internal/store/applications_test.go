package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
)

func TestApplicationRegistryCapacityAndArchiveTombstones(t *testing.T) {
	s, owner, host := taskFixture(t)
	ctx := t.Context()
	c := applicationConfig()
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	// Seed a full host without repeating unrelated audit/session behavior.
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		for i := 0; i < 128; i++ {
			if _, err := tx.Exec("INSERT INTO applications(host_id,name,config,revision,created_at,updated_at) VALUES(?,?,?,1,?,?)", host.ID, fmt.Sprintf("app-%d", i), string(encoded), testTime.Unix(), testTime.Unix()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutApplication(ctx, owner.Token, owner.CSRF, host.ID, "overflow", 0, c, testTime); !errors.Is(err, ErrConflict) {
		t.Fatal("active capacity", err)
	}
	if _, err := s.ArchiveApplication(ctx, owner.Token, owner.CSRF, host.ID, "app-0", 1, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutApplication(ctx, owner.Token, owner.CSRF, host.ID, "replacement", 0, c, testTime); err != nil {
		t.Fatal("archive did not free active capacity", err)
	}
	if _, err := s.PutApplication(ctx, owner.Token, owner.CSRF, host.ID, "app-0", 0, c, testTime); !errors.Is(err, ErrConflict) {
		t.Fatal("tombstone reused", err)
	}
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE applications SET archived=1 WHERE host_id=?", host.ID); err != nil {
			return err
		}
		for i := 129; i < 1024; i++ {
			if _, err := tx.Exec("INSERT INTO applications(host_id,name,config,revision,archived,created_at,updated_at) VALUES(?,?,?,2,1,?,?)", host.ID, fmt.Sprintf("old-%d", i), string(encoded), testTime.Unix(), testTime.Unix()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutApplication(ctx, owner.Token, owner.CSRF, host.ID, "overflow", 0, c, testTime); !errors.Is(err, ErrConflict) {
		t.Fatal("tombstone capacity", err)
	}
}

func applicationConfig() ApplicationConfig {
	return ApplicationConfig{Runtime: "python", ReadRoles: []rbac.Role{rbac.Admin, rbac.Operator, rbac.Viewer}, OperateRoles: []rbac.Role{rbac.Admin, rbac.Operator}}
}

func TestApplicationRevisionReplayArchiveAndAuditRollback(t *testing.T) {
	s, owner, host := taskFixture(t)
	ctx := t.Context()
	put := func(expected int64, c ApplicationConfig) (Application, error) {
		return s.PutApplication(ctx, owner.Token, owner.CSRF, host.ID, "example", expected, c, testTime)
	}
	c := applicationConfig()
	a, err := put(0, c)
	if err != nil || a.Revision != 1 {
		t.Fatal(a, err)
	}
	// Role order is immaterial on a retry.
	c.ReadRoles = []rbac.Role{rbac.Viewer, rbac.Operator, rbac.Admin}
	if replay, err := put(0, c); err != nil || replay.Revision != 1 {
		t.Fatal("create retry", replay, err)
	}
	c.Runtime = "node"
	if _, err := put(0, c); !errors.Is(err, ErrConflict) {
		t.Fatal("changed create", err)
	}
	if _, err := s.PutApplication(ctx, owner.Token, "wrong", host.ID, "example", 1, c, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal("csrf", err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER application_audit_fail BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := put(1, c); err == nil {
		t.Fatal("audit failure accepted")
	}
	if _, err := s.ArchiveApplication(ctx, owner.Token, owner.CSRF, host.ID, "example", 1, testTime); err == nil {
		t.Fatal("archive audit failure accepted")
	}
	got, err := s.Application(ctx, owner.Token, host.ID, "example", testTime)
	if err != nil || got.Revision != 1 || got.Config.Runtime != "python" {
		t.Fatal("rollback", got, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER application_audit_fail"); err != nil {
		t.Fatal(err)
	}
	updated, err := put(1, c)
	if err != nil || updated.Revision != 2 {
		t.Fatal(updated, err)
	}
	if replay, err := put(1, c); err != nil || replay.Revision != 2 {
		t.Fatal("update retry", replay, err)
	}
	c.Runtime = "binary"
	if _, err := put(1, c); !errors.Is(err, ErrConflict) {
		t.Fatal("stale update", err)
	}
	if _, err := s.ArchiveApplication(ctx, owner.Token, owner.CSRF, host.ID, "example", 1, testTime); !errors.Is(err, ErrConflict) {
		t.Fatal("stale archive", err)
	}
	archived, err := s.ArchiveApplication(ctx, owner.Token, owner.CSRF, host.ID, "example", 2, testTime)
	if err != nil || !archived.Archived || archived.Revision != 3 {
		t.Fatal(archived, err)
	}
	if _, err := s.ArchiveApplication(ctx, owner.Token, owner.CSRF, host.ID, "example", 2, testTime); err != nil {
		t.Fatal("archive replay", err)
	}
	if _, err := put(0, c); !errors.Is(err, ErrConflict) {
		t.Fatal("tombstone reused", err)
	}
	if _, err := s.Application(ctx, owner.Token, host.ID, "example", testTime); !errors.Is(err, ErrNotFound) {
		t.Fatal("archived visible", err)
	}
	if list, err := s.Applications(ctx, owner.Token, host.ID, testTime); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM audit_events WHERE action='application.put' AND host_id=? AND json_extract(details,'$.before.revision')=1 AND json_extract(details,'$.after.revision')=2 AND json_extract(details,'$.changed')=1", host.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("scoped before/after audit", count, err)
	}
}

func TestApplicationScopePolicyRevocationAndDisabledUser(t *testing.T) {
	s, owner, a := taskFixture(t)
	ctx := t.Context()
	b, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "second-host", testTime)
	if err != nil {
		t.Fatal(err)
	}
	member, user := inviteUser(t, s, owner, "app-mixed", rbac.Grant{Role: rbac.Admin, HostID: a.ID}, rbac.Grant{Role: rbac.Viewer, HostID: b.ID})
	c := applicationConfig()
	c.ReadRoles = []rbac.Role{rbac.Admin, rbac.Operator}
	for _, host := range []Host{a, b} {
		if _, err := s.PutApplication(ctx, owner.Token, owner.CSRF, host.ID, "hidden", 0, c, testTime); err != nil {
			t.Fatal(err)
		}
	}
	if list, err := s.Applications(ctx, user.Token, b.ID, testTime); err != nil || len(list) != 0 {
		t.Fatal("policy list leak", list, err)
	}
	if _, err := s.Application(ctx, user.Token, b.ID, "hidden", testTime); !errors.Is(err, ErrNotFound) {
		t.Fatal("mixed grant leak", err)
	}
	if _, err := s.PutApplication(ctx, user.Token, user.CSRF, b.ID, "new", 0, c, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal("mixed role/scope", err)
	}
	c.ReadRoles = append(c.ReadRoles, rbac.Viewer)
	if _, err := s.PutApplication(ctx, owner.Token, owner.CSRF, b.ID, "hidden", 1, c, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Application(ctx, user.Token, b.ID, "hidden", testTime); err != nil {
		t.Fatal("viewer read", err)
	}
	if _, err := s.ArchiveApplication(ctx, user.Token, user.CSRF, b.ID, "hidden", 2, testTime); !errors.Is(err, ErrDenied) {
		t.Fatal("viewer archive", err)
	}
	if err := s.SetUserGrants(ctx, owner.Token, owner.CSRF, member.ID, []rbac.Grant{{Role: rbac.Admin, HostID: a.ID}}, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Application(ctx, user.Token, b.ID, "hidden", testTime); !errors.Is(err, ErrNotFound) {
		t.Fatal("scope revoke", err)
	}
	if _, err := s.Application(ctx, user.Token, a.ID, "hidden", testTime); err != nil {
		t.Fatal("manager read", err)
	}
	if err := s.SetUserEnabled(ctx, owner.Token, owner.CSRF, member.ID, false, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Application(ctx, user.Token, a.ID, "hidden", testTime); !errors.Is(err, ErrCredentials) {
		t.Fatal("disabled read", err)
	}
	if err := s.RevokeHost(ctx, owner.Token, owner.CSRF, a.ID, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutApplication(ctx, owner.Token, owner.CSRF, a.ID, "other", 0, c, testTime); !errors.Is(err, ErrConflict) {
		t.Fatal("revoked mutation", err)
	}
}

func TestApplicationValidationAndConcurrentUpdates(t *testing.T) {
	s, owner, host := taskFixture(t)
	for _, name := range []string{"../escape", "Example", "example-", "", "a/b"} {
		if _, err := s.PutApplication(t.Context(), owner.Token, owner.CSRF, host.ID, name, 0, applicationConfig(), testTime); !errors.Is(err, ErrInput) {
			t.Fatal(name, err)
		}
	}
	for _, config := range []ApplicationConfig{
		{Runtime: "shell"},
		{Runtime: "python", ReadRoles: []rbac.Role{rbac.Owner}},
		{Runtime: "python", ReadRoles: []rbac.Role{rbac.Viewer}, OperateRoles: []rbac.Role{rbac.Viewer}},
		{Runtime: "python", OperateRoles: []rbac.Role{rbac.Operator}},
		{Runtime: "python", ReadRoles: []rbac.Role{rbac.Admin, rbac.Admin}},
	} {
		if _, err := s.PutApplication(t.Context(), owner.Token, owner.CSRF, host.ID, "invalid", 0, config, testTime); !errors.Is(err, ErrInput) {
			t.Fatal(config, err)
		}
	}
	if _, err := s.PutApplication(t.Context(), owner.Token, owner.CSRF, host.ID, "race", 0, applicationConfig(), testTime); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, runtime := range []string{"node", "binary"} {
		wg.Go(func() {
			c := applicationConfig()
			c.Runtime = runtime
			_, err := s.PutApplication(t.Context(), owner.Token, owner.CSRF, host.ID, "race", 1, c, testTime)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}

func TestApplicationRestartAndSnapshotRestore(t *testing.T) {
	s, owner, path := prepared(t)
	ctx := t.Context()
	host, err := s.CreateHost(ctx, owner.Token, owner.CSRF, "application-host", testTime)
	if err != nil {
		t.Fatal(err)
	}
	c := applicationConfig()
	if _, err := s.PutApplication(ctx, owner.Token, owner.CSRF, host.ID, "persistent", 0, c, testTime); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if replay, err := reopened.PutApplication(ctx, owner.Token, owner.CSRF, host.ID, "persistent", 0, c, testTime); err != nil || replay.Revision != 1 {
		t.Fatal("restart replay", replay, err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
	if err := reopened.Snapshot(ctx, destination); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot(ctx, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	counts, err := restored.BackupCounts(ctx)
	if err != nil || counts["applications"] != 1 {
		t.Fatal(counts, err)
	}
	if err := restored.PrepareRestoredState(ctx, "application-restore", testTime.Add(time.Second), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Application(ctx, owner.Token, host.ID, "persistent", testTime); !errors.Is(err, ErrCredentials) {
		t.Fatal("old session restored", err)
	}
	session, err := restored.Login(ctx, "owner", testPassword, testTime)
	if err != nil {
		t.Fatal(err)
	}
	if a, err := restored.Application(ctx, session.Token, host.ID, "persistent", testTime); err != nil || a.Revision != 1 || a.Config.Runtime != "python" {
		t.Fatal(a, err)
	}
}

func TestApplicationMigrationAtomicity(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			path, legacy := legacyDatabase(t)
			for i := 1; i < 14; i++ {
				data, err := migrations.ReadFile("migrations/" + migrationNames[i])
				if err != nil {
					t.Fatal(err)
				}
				if _, err := legacy.Exec(string(data)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := legacy.Exec("PRAGMA user_version=14"); err != nil {
				t.Fatal(err)
			}
			if fail {
				if _, err := legacy.Exec("CREATE TABLE applications(conflict TEXT)"); err != nil {
					t.Fatal(err)
				}
			}
			legacy.Close()
			s, err := Open(path)
			if !fail {
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				return
			}
			if err == nil {
				s.Close()
				t.Fatal("migration accepted conflicting table")
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var version int
			if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 14 {
				t.Fatal("partial migration", version, err)
			}
		})
	}
}
