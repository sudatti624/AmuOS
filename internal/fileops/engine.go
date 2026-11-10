package fileops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sudatti624/AmuOS/internal/security"
)

type mountedRoot struct {
	root     *os.Root
	readOnly bool
}
type Engine struct {
	mu    sync.Mutex
	roots map[string]mountedRoot
}

// New pins root directory handles for the process lifetime. Protected paths
// cannot overlap a published root in either direction.
func New(config map[string]RootConfig, protected ...string) (*Engine, error) {
	e := &Engine{roots: map[string]mountedRoot{}}
	fail := func(err error) (*Engine, error) { e.Close(); return nil, err }
	for name, cfg := range config {
		if !ValidName(name) || !filepath.IsAbs(cfg.Path) {
			return fail(errors.New("file roots require an alias and absolute path"))
		}
		absolute, err := filepath.EvalSymlinks(cfg.Path)
		if err != nil {
			return fail(err)
		}
		if !strings.EqualFold(filepath.Clean(absolute), filepath.Clean(cfg.Path)) {
			return fail(errors.New("file root must not use links"))
		}
		for _, p := range protected {
			if p == "" {
				continue
			}
			p, err = filepath.Abs(p)
			if err != nil {
				return fail(err)
			}
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				p = resolved
			}
			if overlaps(absolute, p) {
				return fail(errors.New("file root overlaps protected state"))
			}
		}
		for _, mounted := range e.roots {
			if overlaps(absolute, mounted.root.Name()) {
				return fail(errors.New("file roots overlap"))
			}
		}
		root, err := os.OpenRoot(absolute)
		if err != nil {
			return fail(err)
		}
		e.roots[name] = mountedRoot{root, cfg.ReadOnly}
	}
	return e, nil
}
func overlaps(a, b string) bool {
	a = strings.ToLower(filepath.Clean(a))
	b = strings.ToLower(filepath.Clean(b))
	return a == b || strings.HasPrefix(a, strings.TrimRight(b, string(os.PathSeparator))+string(os.PathSeparator)) || strings.HasPrefix(b, strings.TrimRight(a, string(os.PathSeparator))+string(os.PathSeparator))
}
func (e *Engine) Close() {
	for _, r := range e.roots {
		r.root.Close()
	}
}
func (e *Engine) Enabled() bool { return e != nil && len(e.roots) > 0 }

func checked(root *os.Root, p string, missing bool) (os.FileInfo, error) {
	parts := strings.Split(p, "/")
	current := ""
	var info os.FileInfo
	for i, part := range parts {
		current = path.Join(current, part)
		var err error
		info, err = root.Lstat(current)
		if missing && i == len(parts)-1 && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() || unsafeInfo(info) {
			return nil, errors.New("unsafe file type")
		}
	}
	return info, nil
}
func fileData(root *os.Root, p string) ([]byte, os.FileInfo, error) {
	before, err := checked(root, p, false)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > MaxFile {
		return nil, nil, errors.New("not a bounded regular file")
	}
	f, err := root.Open(p)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(before, info) || unsafeFile(f, info) {
		return nil, nil, errors.New("file changed or has multiple links")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFile+1))
	if err != nil || len(data) > MaxFile {
		return nil, nil, errors.New("file exceeds limit")
	}
	after, err := f.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, nil, errors.New("file changed")
	}
	return data, after, nil
}
func entry(root *os.Root, p string) (Entry, error) {
	info, err := checked(root, p, false)
	if err != nil {
		return Entry{}, err
	}
	seed := []byte(fmt.Sprintf("%s/%d/%d/%d", p, info.Size(), info.ModTime().UnixNano(), info.Mode()))
	if info.Mode().IsRegular() && info.Size() <= MaxFile {
		var data []byte
		data, info, err = fileData(root, p)
		if err != nil {
			return Entry{}, err
		}
		seed = append(seed, data...)
	}
	hash := sha256.Sum256(seed)
	return Entry{Name: path.Base(p), Directory: info.IsDir(), Size: info.Size(), Modified: info.ModTime().Unix(), ETag: hex.EncodeToString(hash[:])}, nil
}

// Execute serializes operations against all roots. The OS must exclude other
// untrusted writers: an ETag is a precondition, not a filesystem transaction.
func (e *Engine) Execute(ctx context.Context, q Request) Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	result, err := e.execute(ctx, q)
	if err != nil {
		return Result{Error: "file_operation_failed"}
	}
	return result
}
func (e *Engine) execute(ctx context.Context, q Request) (Result, error) {
	if err := q.Validate(); err != nil {
		return Result{}, err
	}
	m, ok := e.roots[q.Root]
	if !ok || m.readOnly && !ReadAction(q.Action) {
		return Result{}, errors.New("root not permitted")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	root := m.root
	if q.Action == "list" {
		info, err := checked(root, q.Path, false)
		if err != nil || !info.IsDir() {
			return Result{}, errors.New("not a directory")
		}
		f, err := root.Open(q.Path)
		if err != nil {
			return Result{}, err
		}
		defer f.Close()
		entries, err := f.ReadDir(MaxEntries + 1)
		if err != nil && !errors.Is(err, io.EOF) {
			return Result{}, err
		}
		if len(entries) > MaxEntries {
			return Result{}, errors.New("too many entries")
		}
		result := Result{Entries: []Entry{}}
		for _, item := range entries {
			p := path.Join(q.Path, item.Name())
			if !ValidPath(p) {
				continue
			}
			value, err := entry(root, p)
			if err != nil {
				continue
			}
			result.Entries = append(result.Entries, value)
		}
		return result, nil
	}
	if q.Action == "read" {
		data, _, err := fileData(root, q.Path)
		if err != nil {
			return Result{}, err
		}
		info, err := entry(root, q.Path)
		return Result{Data: data, Info: &info}, err
	}
	if q.Action == "stat" {
		info, err := entry(root, q.Path)
		return Result{Info: &info}, err
	}
	if _, err := checked(root, path.Dir(q.Path), false); err != nil {
		return Result{}, err
	}
	existing, err := checked(root, q.Path, true)
	if err != nil {
		return Result{}, err
	}
	if q.Action == "create" || q.Action == "mkdir" {
		if existing != nil {
			return Result{}, errors.New("already exists")
		}
	} else {
		current, err := entry(root, q.Path)
		if err != nil || current.ETag != q.ETag {
			return Result{}, errors.New("conflict")
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	switch q.Action {
	case "mkdir":
		err = root.Mkdir(q.Path, 0700)
	case "move":
		target, targetErr := checked(root, q.Destination, true)
		if targetErr != nil || target != nil {
			return Result{}, errors.New("destination unavailable")
		}
		err = root.Rename(q.Path, q.Destination)
	case "delete":
		if !existing.IsDir() {
			if err = backup(root, q.Path); err != nil {
				return Result{}, err
			}
		}
		err = root.Remove(q.Path)
	case "create", "write":
		if q.Action == "write" {
			if !existing.Mode().IsRegular() {
				return Result{}, errors.New("not a file")
			}
			if err = backup(root, q.Path); err != nil {
				return Result{}, err
			}
		}
		tmp := path.Join(path.Dir(q.Path), ".amuos-upload-"+security.Token())
		f, openErr := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if openErr != nil {
			return Result{}, openErr
		}
		defer root.Remove(tmp)
		_, err = f.Write(q.Data)
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
		if err != nil {
			return Result{}, err
		}
		if err = ctx.Err(); err != nil {
			return Result{}, err
		}
		if q.Action == "create" {
			err = root.Link(tmp, q.Path)
		} else {
			err = root.Rename(tmp, q.Path)
		}
	}
	if err != nil {
		return Result{}, err
	}
	parents := []string{path.Dir(q.Path)}
	if q.Action == "move" {
		parents = append(parents, path.Dir(q.Destination))
	}
	for _, name := range parents {
		parent, err := root.OpenRoot(name)
		if err != nil {
			return Result{}, err
		}
		err = syncRoot(parent)
		parent.Close()
		if err != nil {
			return Result{}, err
		}
	}
	return Result{}, nil
}
func backup(root *os.Root, p string) error {
	data, _, err := fileData(root, p)
	if err != nil {
		return err
	}
	if err = root.Mkdir(".amuos-history", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := checked(root, ".amuos-history", false)
	if err != nil || !info.IsDir() {
		return errors.New("invalid history")
	}
	id := security.Token()
	f, err := root.OpenFile(".amuos-history/"+id+".data", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	manifest, _ := json.Marshal(map[string]string{"path": p, "data": id + ".data"})
	f, err = root.OpenFile(".amuos-history/"+id+".json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(manifest)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	history, err := root.OpenRoot(".amuos-history")
	if err != nil {
		return err
	}
	defer history.Close()
	return syncRoot(history)
}
