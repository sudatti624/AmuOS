//go:build !linux && !windows

package fileops

import "os"

func unsafeInfo(i os.FileInfo) bool             { return true }
func unsafeFile(f *os.File, i os.FileInfo) bool { return true }
func syncRoot(r *os.Root) error                 { return os.ErrPermission }
