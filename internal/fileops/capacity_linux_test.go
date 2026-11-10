package fileops

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func TestFileCapacityFailure(t *testing.T) {
	mount := os.Getenv("AMUOS_FAULT_DIR")
	if mount == "" {
		t.Skip("requires a dedicated bounded tmpfs")
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(mount, &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Type != unix.TMPFS_MAGIC || stat.Blocks*uint64(stat.Bsize) > 64<<20 {
		t.Fatal("requires tmpfs <=64 MiB")
	}
	dir, err := os.MkdirTemp(mount, "file-capacity-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	e, err := New(map[string]RootConfig{"work": {Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	q := Request{Root: "work", Action: "create", Path: "original", Data: []byte("keep-original")}
	if got := e.Execute(ctx, q); got.Error != "" {
		t.Fatal(got)
	}
	read := e.Execute(ctx, Request{Root: "work", Action: "read", Path: "original"})
	if read.Error != "" {
		t.Fatal(read)
	}
	filler, err := os.Create(filepath.Join(dir, "filler"))
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err = filler.Write(make([]byte, 4096))
		if err != nil {
			break
		}
	}
	filler.Close()
	if !errors.Is(err, unix.ENOSPC) {
		t.Fatal(err)
	}
	q = Request{Root: "work", Action: "write", Path: "original", ETag: read.Info.ETag, Confirm: true, Data: bytes.Repeat([]byte("x"), MaxFile)}
	if got := e.Execute(ctx, q); got.Error == "" {
		t.Fatal("write succeeded on full filesystem")
	}
	original, err := os.ReadFile(filepath.Join(dir, "original"))
	if err != nil || string(original) != "keep-original" {
		t.Fatal("original changed", err)
	}
	if got := e.Execute(ctx, Request{Root: "work", Action: "create", Path: "partial", Data: bytes.Repeat([]byte("x"), MaxFile)}); got.Error == "" {
		t.Fatal("upload succeeded on full filesystem")
	}
	if _, err := os.Stat(filepath.Join(dir, "partial")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial file published")
	}
	if err := os.Remove(filepath.Join(dir, "filler")); err != nil {
		t.Fatal(err)
	}
	if got := e.Execute(ctx, q); got.Error != "" {
		t.Fatal("retry after freeing space", got)
	}
}
