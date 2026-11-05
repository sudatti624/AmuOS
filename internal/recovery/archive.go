// Package recovery implements stopped, local maintenance. No network API is
// provided, and all callers must hold the state-directory lock.
package recovery

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"
	"github.com/sudatti624/AmuOS/internal/store"
)

const (
	maxDatabase  = int64(10 << 30)
	maxSmallFile = int64(1 << 20)
	maxManifest  = int64(64 << 10)
	maxArchive   = int64(12 << 30)
)

type Config struct {
	FormatVersion int    `json:"format_version"`
	Address       string `json:"address"`
	Origin        string `json:"origin"`
}

func (c Config) validate() error {
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || c.FormatVersion != 1 {
		return errors.New("invalid backup configuration")
	}
	if _, _, err := net.SplitHostPort(c.Address); err != nil {
		return errors.New("invalid backup listen address")
	}
	return nil
}

type fileRecord struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Manifest struct {
	FormatVersion  int                   `json:"format_version"`
	ProductVersion string                `json:"product_version"`
	SchemaVersion  int                   `json:"schema_version"`
	ConfigVersion  int                   `json:"config_version"`
	ID             string                `json:"backup_id"`
	Boundary       time.Time             `json:"consistency_boundary"`
	Recipient      string                `json:"recipient"`
	KeyIDs         []string              `json:"key_ids"`
	Files          map[string]fileRecord `json:"files"`
	Counts         map[string]int64      `json:"counts"`
}

// There are no reversible secrets or Agent signing keys in the current schema.
// Fail closed on nonempty bundles until their restoration/rotation is defined.
type keyBundle struct {
	FormatVersion int               `json:"format_version"`
	Keys          []json.RawMessage `json:"keys"`
}

func decode(data []byte, destination any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(destination); err != nil {
		return errors.New("invalid backup JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing backup JSON")
	}
	return nil
}

func (m Manifest) validate(version string) error {
	if m.FormatVersion != 1 || m.ConfigVersion != 1 || m.SchemaVersion != store.SchemaVersion() || m.ProductVersion != version || version == "" {
		return errors.New("backup version is incompatible with this binary")
	}
	if len(m.ID) != 32 || m.Boundary.IsZero() || len(m.KeyIDs) != 0 {
		return errors.New("invalid backup manifest")
	}
	if _, err := hex.DecodeString(m.ID); err != nil {
		return errors.New("invalid backup ID")
	}
	if _, err := age.ParseX25519Recipient(m.Recipient); err != nil {
		return errors.New("invalid backup recipient")
	}
	if len(m.Files) != 3 {
		return errors.New("invalid backup file list")
	}
	if len(m.Counts) != 13 {
		return errors.New("invalid backup record counts")
	}
	for _, count := range m.Counts {
		if count < 0 {
			return errors.New("invalid backup record count")
		}
	}
	for _, name := range []string{"database.sqlite", "config.json", "keys.json"} {
		r, ok := m.Files[name]
		limit := maxSmallFile
		if name == "database.sqlite" {
			limit = maxDatabase
		}
		if !ok || r.Size <= 0 || r.Size > limit || len(r.SHA256) != 64 {
			return errors.New("invalid backup file declaration")
		}
		if _, err := hex.DecodeString(r.SHA256); err != nil {
			return errors.New("invalid backup hash")
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func describe(ctx context.Context, path string) (fileRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileRecord{}, err
	}
	defer f.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, contextReader{ctx, io.LimitReader(f, maxDatabase+1)})
	if err != nil {
		return fileRecord{}, err
	}
	if size > maxDatabase {
		return fileRecord{}, errors.New("backup database exceeds size limit")
	}
	return fileRecord{Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func record(data []byte) fileRecord {
	hash := sha256.Sum256(data)
	return fileRecord{Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}
}

func writeArchive(ctx context.Context, out io.Writer, recipient age.Recipient, verification age.Recipient, manifest Manifest, database string, config, keys []byte) error {
	encrypted, err := age.Encrypt(out, recipient, verification)
	if err != nil {
		return err
	}
	tarball := tar.NewWriter(encrypted)
	write := func(name string, size int64, reader io.Reader) error {
		if err := tarball.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		n, err := io.Copy(tarball, contextReader{ctx, reader})
		if err != nil {
			return err
		}
		if n != size {
			return errors.New("backup source changed during capture")
		}
		return nil
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := write("manifest.json", int64(len(encoded)), bytes.NewReader(encoded)); err != nil {
		return err
	}
	f, err := os.Open(database)
	if err != nil {
		return err
	}
	err = write("database.sqlite", manifest.Files["database.sqlite"].Size, f)
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	for _, item := range []struct {
		name string
		data []byte
	}{{"config.json", config}, {"keys.json", keys}} {
		if err := write(item.name, int64(len(item.data)), bytes.NewReader(item.data)); err != nil {
			return err
		}
	}
	if err := tarball.Close(); err != nil {
		return err
	}
	return encrypted.Close()
}

// readArchive authenticates through encrypted EOF, not just the tar end marker.
// Staging never uses archive entry names as filesystem paths. An empty directory
// validates the stream without materializing a second database.
func readArchive(ctx context.Context, input io.Reader, identity age.Identity, version, directory string) (Manifest, Config, error) {
	var manifest Manifest
	var config Config
	fail := func(err error) (Manifest, Config, error) { return Manifest{}, Config{}, err }
	plain, err := age.Decrypt(contextReader{ctx, input}, identity)
	if err != nil {
		return fail(errors.New("cannot decrypt backup"))
	}
	reader := tar.NewReader(plain)
	validHeader := func(h *tar.Header) bool {
		return h.Typeflag == tar.TypeReg && h.Format == tar.FormatUSTAR && len(h.PAXRecords) == 0 && h.Linkname == ""
	}
	header, err := reader.Next()
	if err != nil || header.Name != "manifest.json" || !validHeader(header) || header.Size <= 0 || header.Size > maxManifest {
		return fail(errors.New("invalid backup manifest entry"))
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return fail(err)
	}
	if err := decode(data, &manifest); err != nil {
		return fail(err)
	}
	if err := manifest.validate(version); err != nil {
		return fail(err)
	}
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(err)
		}
		expected, ok := manifest.Files[header.Name]
		if !ok || seen[header.Name] || !validHeader(header) || header.Size != expected.Size {
			return fail(errors.New("invalid or duplicate backup entry"))
		}
		seen[header.Name] = true
		hash := sha256.New()
		var small bytes.Buffer
		var destination io.Writer = io.Discard
		var file *os.File
		if header.Name == "database.sqlite" && directory != "" {
			file, err = os.OpenFile(filepath.Join(directory, "database.sqlite"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return fail(err)
			}
			destination = file
		} else if header.Name != "database.sqlite" {
			destination = &small
		}
		n, copyErr := io.Copy(io.MultiWriter(hash, destination), contextReader{ctx, reader})
		if file != nil {
			if copyErr == nil {
				copyErr = file.Sync()
			}
			copyErr = errors.Join(copyErr, file.Close())
		}
		if copyErr != nil {
			return fail(copyErr)
		}
		if n != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fail(errors.New("backup content hash mismatch"))
		}
		if header.Name == "config.json" {
			if err := decode(small.Bytes(), &config); err != nil {
				return fail(err)
			}
			if err := config.validate(); err != nil {
				return fail(err)
			}
		}
		if header.Name == "keys.json" {
			var keys keyBundle
			if err := decode(small.Bytes(), &keys); err != nil {
				return fail(err)
			}
			if keys.FormatVersion != 1 || keys.Keys == nil || len(keys.Keys) != 0 {
				return fail(errors.New("unsupported backup key bundle"))
			}
		}
	}
	if len(seen) != 3 {
		return fail(errors.New("backup is missing required files"))
	}
	// A bounded zero tar pad is allowed. Reading to EOF is mandatory to check
	// the final age chunk; ignore neither truncation nor bytes after the tar.
	tail, err := io.ReadAll(io.LimitReader(contextReader{ctx, plain}, 1025))
	if err != nil {
		return fail(err)
	}
	if len(tail) > 1024 || !bytes.Equal(tail, make([]byte, len(tail))) {
		return fail(errors.New("trailing backup data"))
	}
	return manifest, config, nil
}
