package statelock

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectoryExclusionAndRelease(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "amuos.db")
	first, err := Acquire(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	// Different database names in the same state directory must also conflict.
	if second, err := Acquire(filepath.Join(directory, "other.db")); !errors.Is(err, ErrLocked) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second acquisition: %v", err)
	}
	independent, err := Acquire(filepath.Join(t.TempDir(), "amuos.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = independent.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".amuos.lock")); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(database)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}

func TestInvalidPaths(t *testing.T) {
	for _, path := range []string{"", ":memory:", "file:state.db"} {
		if acquired, err := Acquire(path); err == nil {
			_ = acquired.Close()
			t.Fatalf("accepted %q", path)
		}
	}
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, ".amuos.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	if acquired, err := Acquire(filepath.Join(directory, "amuos.db")); err == nil {
		_ = acquired.Close()
		t.Fatal("accepted directory as lock file")
	}
}

func TestLockProcess(t *testing.T) {
	database := os.Getenv("AMUOS_LOCK_TEST_DATABASE")
	if database == "" {
		return
	}
	acquired, err := Acquire(database)
	if err != nil {
		t.Fatal(err)
	}
	defer acquired.Close()
	_, _ = os.Stdout.WriteString("locked\n")
	// Parent kills this process without running deferred cleanup.
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func TestProcessCrashReleasesLock(t *testing.T) {
	database := filepath.Join(t.TempDir(), "state", "amuos.db")
	child := exec.Command(os.Args[0], "-test.run=^TestLockProcess$")
	child.Env = append(os.Environ(), "AMUOS_LOCK_TEST_DATABASE="+database)
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
		if line != "locked\n" {
			t.Fatalf("child failed to lock: %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child lock timeout")
	}
	if acquired, err := Acquire(database); !errors.Is(err, ErrLocked) {
		if acquired != nil {
			_ = acquired.Close()
		}
		t.Fatalf("cross-process acquisition: %v", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	again, err := Acquire(database)
	if err != nil {
		t.Fatalf("lock after crash: %v", err)
	}
	_ = again.Close()
}
