package fileops

import (
	"golang.org/x/sys/windows"
	"os"
	"syscall"
)

func unsafeInfo(i os.FileInfo) bool {
	s, ok := i.Sys().(*syscall.Win32FileAttributeData)
	return ok && s.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
func unsafeFile(f *os.File, i os.FileInfo) bool {
	var s windows.ByHandleFileInformation
	return windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &s) != nil || s.NumberOfLinks != 1 || s.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
func syncRoot(r *os.Root) error { return nil }
