// Package fileops implements bounded operations within explicitly configured roots.
package fileops

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

const MaxFile = 256 << 10
const MaxBody = 2 << 20
const MaxEntries = 1000

type RootConfig struct {
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only"`
}
type Request struct {
	Root        string `json:"root"`
	Action      string `json:"action"`
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`
	Data        []byte `json:"data,omitempty"`
	ETag        string `json:"etag,omitempty"`
	Confirm     bool   `json:"confirm,omitempty"`
}
type Entry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
	Modified  int64  `json:"modified"`
	ETag      string `json:"etag"`
}
type Result struct {
	Entries []Entry `json:"entries,omitempty"`
	Info    *Entry  `json:"info,omitempty"`
	Data    []byte  `json:"data,omitempty"`
	Error   string  `json:"error,omitempty"`
}

func ReadAction(action string) bool { return action == "list" || action == "stat" || action == "read" }
func ValidName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func ValidPath(p string) bool {
	if p == "." {
		return true
	}
	if p == "" || len(p) > 1024 || !utf8.ValidString(p) || path.IsAbs(p) || path.Clean(p) != p || strings.ContainsAny(p, "\\:\x00") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || strings.HasPrefix(strings.ToLower(part), ".amuos-") || strings.TrimRight(part, ". ") != part {
			return false
		}
		for _, c := range part {
			if c < 32 || strings.ContainsRune("<>\"|?*", c) {
				return false
			}
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" || strings.HasPrefix(base, "COM") && len(base) == 4 || strings.HasPrefix(base, "LPT") && len(base) == 4 {
			return false
		}
	}
	return true
}
func (q Request) Validate() error {
	if !ValidName(q.Root) || !ValidPath(q.Path) || len(q.Data) > MaxFile {
		return errors.New("invalid file request")
	}
	switch q.Action {
	case "list", "stat", "read":
		if q.Destination != "" || len(q.Data) > 0 || q.ETag != "" || q.Confirm {
			return errors.New("unexpected input")
		}
	case "create", "mkdir":
		if q.Path == "." || q.Destination != "" || q.ETag != "" || q.Confirm || q.Action == "mkdir" && len(q.Data) > 0 {
			return errors.New("invalid create")
		}
	case "write", "move", "delete":
		if q.Action == "write" && (!utf8.Valid(q.Data) || strings.ContainsRune(string(q.Data), 0)) {
			return errors.New("UTF-8 text required")
		}
		if q.Path == "." || !q.Confirm || len(q.ETag) != 64 {
			return errors.New("confirmation and etag required")
		}
		if q.Action == "move" {
			if !ValidPath(q.Destination) || q.Destination == "." || q.Destination == q.Path || len(q.Data) > 0 {
				return errors.New("invalid destination")
			}
		} else if q.Destination != "" {
			return errors.New("unexpected destination")
		}
		if q.Action == "delete" && len(q.Data) > 0 {
			return errors.New("unexpected data")
		}
	default:
		return errors.New("unsupported file operation")
	}
	return nil
}
