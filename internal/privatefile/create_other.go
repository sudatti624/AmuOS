//go:build !windows

// Package privatefile creates secret files without an inherited public ACL.
package privatefile

import "os"

func Create(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}
