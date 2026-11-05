package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckUpdateDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "amuos.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	check, err := CheckUpdate(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if check.DatabaseSchema != SchemaVersion() || check.MigrationRequired {
		t.Fatalf("current database: %+v", check)
	}

	setVersion := func(version int) {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
			t.Fatal(err)
		}
	}
	readVersion := func() int {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var version int
		if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		return version
	}

	// An older schema is reported, and the check itself must not migrate it.
	setVersion(SchemaVersion() - 1)
	check, err = CheckUpdate(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if !check.MigrationRequired || check.DatabaseSchema != SchemaVersion()-1 {
		t.Fatalf("older database: %+v", check)
	}
	if got := readVersion(); got != SchemaVersion()-1 {
		t.Fatalf("check migrated the database to %d", got)
	}

	setVersion(SchemaVersion() + 1)
	if _, err := CheckUpdate(ctx, path); err == nil {
		t.Fatal("accepted a newer schema")
	}
	setVersion(0)
	if _, err := CheckUpdate(ctx, path); err == nil {
		t.Fatal("accepted an uninitialized database")
	}
}

func TestCheckUpdateRejectsMissingAndInvalidFiles(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	missing := filepath.Join(directory, "missing.db")
	if _, err := CheckUpdate(ctx, missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database: %v", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("check created a database")
	}
	if _, err := CheckUpdate(ctx, directory); err == nil {
		t.Fatal("accepted a directory")
	}
	garbage := filepath.Join(directory, "garbage.db")
	if err := os.WriteFile(garbage, []byte("not a database, just some bytes that are long enough"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckUpdate(ctx, garbage); err == nil {
		t.Fatal("accepted a non-database file")
	}
}
