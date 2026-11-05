// Package statelock serializes local processes that use one state directory.
package statelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrLocked = errors.New("state directory is already in use")

type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

// Acquire locks the directory containing a filesystem database before it is
// opened or migrated. The persistent lock file must never be removed: replacing
// it would let another process lock a different inode in the same directory.
func Acquire(database string) (*Lock, error) {
	if database == "" || database == ":memory:" || strings.HasPrefix(database, "file:") {
		return nil, errors.New("state lock requires a filesystem database path")
	}
	directory := filepath.Dir(database)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, ".amuos.lock")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("state lock must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lock(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire state lock: %w", err)
	}
	return &Lock{file: file}, nil
}

// Close releases the OS lock. Process termination also releases it, including
// abrupt termination; the presence of the file does not indicate ownership.
func (l *Lock) Close() error {
	l.once.Do(func() { l.err = l.file.Close() })
	return l.err
}
