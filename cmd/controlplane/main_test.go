package main

import (
	"context"
	"errors"
	"flag"
	"github.com/sudatti624/AmuOS/internal/recovery"
	"github.com/sudatti624/AmuOS/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sudatti624/AmuOS/internal/statelock"
)

func runWithArgs(t *testing.T, args ...string) error {
	t.Helper()
	oldArgs, oldFlags := os.Args, flag.CommandLine
	os.Args = append([]string{"controlplane"}, args...)
	flag.CommandLine = flag.NewFlagSet("controlplane", flag.ContinueOnError)
	defer func() { os.Args, flag.CommandLine = oldArgs, oldFlags }()
	return run(context.Background())
}

func TestBootstrapCannotModifyLockedState(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "amuos.db")
	token := filepath.Join(directory, "setup-token")
	acquired, err := statelock.Acquire(database)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Close()
	if err := runWithArgs(t, "--db", database, "--bootstrap-file", token); !errors.Is(err, statelock.ErrLocked) {
		t.Fatalf("locked bootstrap: %v", err)
	}
	for _, path := range []string{database, token} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("locked startup created state: %v", err)
		}
	}
	if err := acquired.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runWithArgs(t, "--db", database, "--bootstrap-file", token); err != nil {
		t.Fatal(err)
	}
	// A failed subsequent bootstrap must also release the lock.
	if err := runWithArgs(t, "--db", database, "--bootstrap-file", token); err == nil {
		t.Fatal("overwrote bootstrap")
	}
	again, err := statelock.Acquire(database)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}

func TestInvalidTLSDoesNotCreateLockDirectory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state")
	if err := runWithArgs(t, "--db", filepath.Join(directory, "amuos.db"), "--tls-cert", "missing.pem", "--tls-key", "missing.key"); err == nil {
		t.Fatal("accepted missing TLS files")
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid TLS initialized state: %v", err)
	}
}

func TestMaintenanceModesAndCLIRoundTrip(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "source", "amuos.db")
	for _, args := range [][]string{
		{"--backup-out", "out.age"},
		{"--recipient", "age1invalid"},
		{"--restore-from", "in.age"},
		{"--recovery-key-file", "key"},
		{"--backup-out", "out.age", "--recipient", "bad", "--bootstrap-file", "token"},
		{"--restore-from", "in.age", "--recovery-key-file", "key", "--version"},
		{"--generate-recovery-key", "key", "--bootstrap-file", "token"},
	} {
		if err := runWithArgs(t, append([]string{"--db", database}, args...)...); err == nil {
			t.Fatalf("accepted invalid mode: %v", args)
		}
	}
	if _, err := os.Stat(filepath.Dir(database)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid mode created state")
	}
	keyFile := filepath.Join(root, "recovery.key")
	if err := runWithArgs(t, "--generate-recovery-key", keyFile); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := runWithArgs(t, "--generate-recovery-key", keyFile); err == nil {
		t.Fatal("overwrote recovery key")
	}
	after, _ := os.ReadFile(keyFile)
	if string(key) != string(after) {
		t.Fatal("recovery identity changed")
	}
	// Recover the public recipient without ever printing the private key.
	lines := strings.Split(strings.TrimSpace(string(key)), "\n")
	if len(lines) != 2 {
		t.Fatal("invalid generated identity")
	}
	// The public recipient is independently recovered through age's parser in
	// the recovery package tests; generate a second key for this CLI round trip.
	otherKey := filepath.Join(root, "cli.key")
	recipient, err := recovery.GenerateRecoveryKey(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := runWithArgs(t, "--db", database, "--bootstrap-file", filepath.Join(root, "setup-token")); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "backup.age")
	if err := runWithArgs(t, "--db", database, "--backup-out", archive, "--recipient", recipient); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "restored", "amuos.db")
	if err := runWithArgs(t, "--db", target, "--restore-from", archive, "--recovery-key-file", otherKey); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A backup of a not-yet-configured instance cannot revive its old token.
	if _, err := db.PrepareBootstrap(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDBDoesNotCreateOrShareState(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "state", "amuos.db")
	if err := runWithArgs(t, "--db", database, "--check-db"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing database: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(database)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("check created a state directory")
	}
	if err := runWithArgs(t, "--check-db", "--version"); err == nil {
		t.Fatal("accepted two modes")
	}
	if err := runWithArgs(t, "--db", database, "--bootstrap-file", filepath.Join(root, "setup-token")); err != nil {
		t.Fatal(err)
	}
	acquired, err := statelock.Acquire(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := runWithArgs(t, "--db", database, "--check-db"); !errors.Is(err, statelock.ErrLocked) {
		t.Fatalf("check ran while the state was in use: %v", err)
	}
	if err := acquired.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runWithArgs(t, "--db", database, "--check-db"); err != nil {
		t.Fatal(err)
	}
}
