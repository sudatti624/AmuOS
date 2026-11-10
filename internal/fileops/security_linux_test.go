package fileops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileOSPermissionsAndLinkReplacement(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks require an unprivileged user")
	}
	dir := t.TempDir()
	e, err := New(map[string]RootConfig{"work": {Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if result := e.Execute(context.Background(), Request{Root: "work", Action: "create", Path: "denied"}); result.Error == "" {
		t.Fatal("OS write permission bypassed")
	}
	os.Chmod(dir, 0700)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0600)
	inside := filepath.Join(dir, "swap")
	os.Mkdir(inside, 0700)
	os.WriteFile(filepath.Join(inside, "secret"), []byte("inside"), 0600)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			default:
			}
			if os.Rename(inside, inside+"-parked") == nil {
				os.Symlink(outside, inside)
				os.Remove(inside)
				os.Rename(inside+"-parked", inside)
			}
		}
	}()
	for i := 0; i < 250; i++ {
		result := e.Execute(context.Background(), Request{Root: "work", Action: "read", Path: "swap/secret"})
		if result.Error == "" && string(result.Data) != "inside" {
			close(done)
			<-stopped
			t.Fatal("replacement escaped root")
		}
	}
	close(done)
	<-stopped
}
