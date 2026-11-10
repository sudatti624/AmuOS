package fileops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileLifecycleConflictReadOnlyAndRecovery(t *testing.T) {
	dir := t.TempDir()
	e, err := New(map[string]RootConfig{"work": {Path: dir}, "read": {Path: t.TempDir(), ReadOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	run := func(q Request) Result {
		t.Helper()
		q.Root = "work"
		r := e.Execute(context.Background(), q)
		if r.Error != "" {
			t.Fatalf("%s %s: %s", q.Action, q.Path, r.Error)
		}
		return r
	}
	run(Request{Action: "mkdir", Path: "folder"})
	directory := run(Request{Action: "stat", Path: "folder"})
	run(Request{Action: "move", Path: "folder", Destination: "renamed-folder", ETag: directory.Info.ETag, Confirm: true})
	directory = run(Request{Action: "stat", Path: "renamed-folder"})
	run(Request{Action: "move", Path: "renamed-folder", Destination: "folder", ETag: directory.Info.ETag, Confirm: true})
	run(Request{Action: "create", Path: "folder/a.txt", Data: []byte("original")})
	r := run(Request{Action: "read", Path: "folder/a.txt"})
	if string(r.Data) != "original" {
		t.Fatal(r)
	}
	edit := Request{Root: "work", Action: "write", Path: "folder/a.txt", Data: []byte("edited"), ETag: r.Info.ETag, Confirm: true}
	if got := e.Execute(context.Background(), edit); got.Error != "" {
		t.Fatal(got)
	}
	if got := e.Execute(context.Background(), edit); got.Error == "" {
		t.Fatal("stale edit accepted")
	}
	r = run(Request{Action: "stat", Path: "folder/a.txt"})
	run(Request{Action: "move", Path: "folder/a.txt", Destination: "b.txt", ETag: r.Info.ETag, Confirm: true})
	r = run(Request{Action: "read", Path: "b.txt"})
	if string(r.Data) != "edited" {
		t.Fatal(r)
	}
	run(Request{Action: "delete", Path: "b.txt", ETag: r.Info.ETag, Confirm: true})
	entries, err := os.ReadDir(filepath.Join(dir, ".amuos-history"))
	if err != nil || len(entries) != 4 {
		t.Fatalf("history %v %v", entries, err)
	}
	result := run(Request{Action: "list", Path: "."})
	if len(result.Entries) != 1 || result.Entries[0].Name != "folder" {
		t.Fatal(result)
	}
	e.Close()
	e, err = New(map[string]RootConfig{"work": {Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if got := e.Execute(context.Background(), Request{Root: "work", Action: "read", Path: "b.txt"}); got.Error == "" {
		t.Fatal("deleted file returned after restart")
	}
	ro, err := New(map[string]RootConfig{"work": {Path: dir, ReadOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if got := ro.Execute(context.Background(), Request{Root: "work", Action: "create", Path: "denied"}); got.Error == "" {
		t.Fatal("readonly mutation")
	}
}
func TestPathsLinksLimitsAndCancellation(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600)
	e, err := New(map[string]RootConfig{"work": {Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, p := range []string{"../secret", "/secret", "x/../secret", "x\\secret", "secret:ads", "CON", "a/..", ".amuos-history/data", "a\x00b", "a.", "a ", "x//y"} {
		if ValidPath(p) {
			t.Errorf("accepted %q", p)
		}
	}
	for _, action := range []string{"read", "create", "mkdir", "delete", "move", "write"} {
		if got := e.Execute(context.Background(), Request{Root: "work", Action: action, Path: "../secret", Confirm: true, ETag: strings.Repeat("a", 64)}); got.Error == "" {
			t.Fatal("escape", action)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err == nil {
		if got := e.Execute(context.Background(), Request{Root: "work", Action: "read", Path: "link/secret"}); got.Error == "" {
			t.Fatal("symlink escaped")
		}
	}
	if err := os.Link(filepath.Join(outside, "secret"), filepath.Join(dir, "hard")); err == nil {
		if got := e.Execute(context.Background(), Request{Root: "work", Action: "read", Path: "hard"}); got.Error == "" {
			t.Fatal("hardlink exposed")
		}
	}
	if got := e.Execute(context.Background(), Request{Root: "work", Action: "create", Path: "large", Data: make([]byte, MaxFile+1)}); got.Error == "" {
		t.Fatal("oversize")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := e.Execute(ctx, Request{Root: "work", Action: "create", Path: "cancelled"}); got.Error == "" {
		t.Fatal("cancelled write")
	}
	if _, err := os.Stat(filepath.Join(dir, "cancelled")); !os.IsNotExist(err) {
		t.Fatal("partial file published")
	}
	if _, err := New(map[string]RootConfig{"work": {Path: dir}}, dir); err == nil {
		t.Fatal("protected root")
	}
	if _, err := New(map[string]RootConfig{"work": {Path: filepath.Dir(dir)}}, dir); err == nil {
		t.Fatal("protected descendant")
	}
	if _, err := New(map[string]RootConfig{"work": {Path: dir}}, filepath.Dir(dir)); err == nil {
		t.Fatal("protected ancestor")
	}
}

func TestFileRootRejectsLinksInAncestors(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(target, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Skipf("directory links require OS support: %v", err)
	}
	for _, root := range []string{alias, filepath.Join(alias, "child")} {
		if engine, err := New(map[string]RootConfig{"work": {Path: root}}); err == nil {
			engine.Close()
			t.Fatalf("accepted directory link %s", root)
		}
	}
}
