package recovery

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sudatti624/AmuOS/internal/store"
	"golang.org/x/sys/unix"
)

// CI supplies a dedicated, bounded tmpfs. Never fill a developer's filesystem.
func TestFoundationCapacityFailure(t *testing.T) {
	root := os.Getenv("AMUOS_FAULT_DIR")
	if root == "" {
		t.Skip("requires dedicated <=64 MiB tmpfs (scripts/check-foundation-capacity.sh)")
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(root, &fs); err != nil {
		t.Fatal(err)
	}
	if fs.Type != unix.TMPFS_MAGIC || fs.Blocks*uint64(fs.Bsize) > 64<<20 {
		t.Fatal("fault directory must be a dedicated tmpfs of at most 64 MiB")
	}
	f := prepared(t)
	dir, err := os.MkdirTemp(root, "amuos-capacity-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	target := filepath.Join(dir, "amuos.db")
	if _, err := Restore(t.Context(), target, f.archive, f.keyFile, "test-version"); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	filler, err := os.Create(filepath.Join(dir, "filler"))
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 4096)
	for {
		if _, err = filler.Write(chunk); err != nil {
			break
		}
	}
	if err := filler.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(err, unix.ENOSPC) {
		t.Fatalf("expected ENOSPC, got %v", err)
	}
	output := filepath.Join(dir, "failed.age")
	if _, err := Backup(t.Context(), f.database, output, f.identity.Recipient().String(), "test-version", testConfig); err == nil {
		t.Fatal("backup on full filesystem succeeded")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial backup published: %v", err)
	}
	if _, err := Restore(t.Context(), target, f.archive, f.keyFile, "test-version"); err == nil {
		t.Fatal("restore on full filesystem succeeded")
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("capacity failure modified existing database", err)
	}
	if err := os.Remove(filepath.Join(dir, "filler")); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateSnapshot(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(t.Context(), target, f.archive, f.keyFile, "test-version"); err != nil {
		t.Fatal("restore did not recover after freeing space", err)
	}
	t.Log("ENOSPC: backup unpublished, existing restore target unchanged, retry after freeing space succeeded")
}
