package fileops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestFileRootWindowsShortName(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "directory with a long name")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	encoded, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	n, err := windows.GetShortPathName(encoded, &buffer[0], uint32(len(buffer)))
	if err != nil || n == 0 || n >= uint32(len(buffer)) {
		t.Fatalf("short path: %d %v", n, err)
	}
	short := windows.UTF16ToString(buffer[:n])
	canonical, err := filepath.EvalSymlinks(short)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(short, canonical) {
		t.Skip("8.3 aliases are not enabled on this volume")
	}
	engine, err := New(map[string]RootConfig{"work": {Path: short}})
	if err != nil {
		t.Fatalf("short name was mistaken for a link: %v", err)
	}
	defer engine.Close()
	result := engine.Execute(context.Background(), Request{Root: "work", Action: "create", Path: "example.txt", Data: []byte("short-name")})
	if result.Error != "" {
		t.Fatal(result)
	}
	data, err := os.ReadFile(filepath.Join(directory, "example.txt"))
	if err != nil || string(data) != "short-name" {
		t.Fatal("wrong directory", err)
	}
	if blocked, err := New(map[string]RootConfig{"work": {Path: short}}, canonical); err == nil {
		blocked.Close()
		t.Fatal("short name bypassed protected directory check")
	}
}

func TestFileRootWindowsJunctionRefused(t *testing.T) {
	base, target := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(target, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "secret"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(base, "junction")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("create NTFS junction: %v %s", err, output)
	}
	defer os.Remove(junction)
	for _, directory := range []string{junction, filepath.Join(junction, "child")} {
		if engine, err := New(map[string]RootConfig{"work": {Path: directory}}); err == nil {
			engine.Close()
			t.Fatal("junction root was accepted")
		}
	}
	engine, err := New(map[string]RootConfig{"work": {Path: base}})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if result := engine.Execute(context.Background(), Request{Root: "work", Action: "read", Path: "junction/secret"}); result.Error == "" {
		t.Fatal("junction content was exposed")
	}
}
