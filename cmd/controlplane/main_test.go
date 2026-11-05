package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/sudatti624/AmuOS/internal/statelock"
)

func runWithArgs(t *testing.T, args ...string) error {
	t.Helper()
	oldArgs, oldFlags := os.Args, flag.CommandLine
	os.Args = append([]string{"controlplane"}, args...)
	flag.CommandLine = flag.NewFlagSet("controlplane", flag.ContinueOnError)
	defer func() { os.Args, flag.CommandLine = oldArgs, oldFlags }()
	return run()
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
