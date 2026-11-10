package fileops

import (
	"os"
	"syscall"
)

func unsafeInfo(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && !i.IsDir() && s.Nlink != 1
}
func unsafeFile(f *os.File, i os.FileInfo) bool { return unsafeInfo(i) }
func syncRoot(r *os.Root) error {
	f, e := r.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
