//go:build !linux

package recovery

// Windows development builds flush files; directory fsync is a Linux
// deployment durability check and has no portable Windows counterpart here.
func syncDirectory(string) error { return nil }
