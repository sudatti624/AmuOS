package privatefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreateNeverOverwritesSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	f, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("secret"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if f, err = Create(path); err == nil {
		f.Close()
		t.Fatal("overwrote secret")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "secret" {
		t.Fatal("secret changed")
	}
}
