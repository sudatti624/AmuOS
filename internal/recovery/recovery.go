package recovery

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/sudatti624/AmuOS/internal/agentca"
	"github.com/sudatti624/AmuOS/internal/privatefile"
	"github.com/sudatti624/AmuOS/internal/statelock"
	"github.com/sudatti624/AmuOS/internal/store"
)

func newID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}

func regular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("maintenance input must be a regular file")
	}
	return nil
}

func scratch(parent, prefix string) (string, func(), error) {
	absolute, err := filepath.Abs(parent)
	if err != nil {
		return "", nil, err
	}
	directory, err := os.MkdirTemp(absolute, prefix)
	if err != nil {
		return "", nil, err
	}
	marker := filepath.Join(directory, ".amuos-stage")
	if err := os.WriteFile(marker, []byte("amuos-maintenance-stage-v1\n"), 0600); err != nil {
		// A full filesystem can create the file but fail to write its contents.
		// Remove that incomplete marker before removing our new directory, so
		// the next maintenance attempt does not encounter an invalid stage.
		_ = os.Remove(marker)
		_ = os.Remove(directory)
		return "", nil, err
	}
	if err := syncFile(marker); err != nil {
		_ = os.Remove(marker)
		_ = os.Remove(directory)
		return "", nil, err
	}
	if err := syncDirectory(directory); err != nil {
		_ = removeStage(absolute, directory)
		return "", nil, err
	}
	if err := syncDirectory(absolute); err != nil {
		_ = removeStage(absolute, directory)
		return "", nil, err
	}
	// Only remove files in the exact marked directory created here.
	cleanup := func() {
		if filepath.Dir(directory) == absolute && strings.HasPrefix(filepath.Base(directory), prefix) {
			_ = removeStage(absolute, directory)
		}
	}
	return directory, cleanup, nil
}

func removeStage(parent, directory string) error {
	if filepath.Dir(directory) != parent {
		return errors.New("invalid maintenance staging path")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid maintenance staging directory")
	}
	marker := filepath.Join(directory, ".amuos-stage")
	if err := regular(marker); err != nil {
		return err
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		return err
	}
	if string(contents) != "amuos-maintenance-stage-v1\n" {
		return errors.New("invalid maintenance staging marker")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case ".amuos-stage", "database.sqlite", "database.sqlite-wal", "database.sqlite-shm", "database.sqlite-journal":
		default:
			return errors.New("unexpected file in maintenance staging directory")
		}
		if err := regular(filepath.Join(directory, entry.Name())); err != nil {
			return err
		}
	}
	for _, entry := range entries {
		if entry.Name() != ".amuos-stage" {
			if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
				return err
			}
		}
	}
	if err := os.Remove(marker); err != nil {
		return err
	}
	return os.Remove(directory)
}

// CleanupStages requires exclusive ownership of the state-directory lock.
// Only marked directories with an exact allowlist of regular files are removed.
// Retained pre-restore snapshots and unmarked directories are never removed.
func CleanupStages(database string) error {
	parent, err := filepath.Abs(filepath.Dir(database))
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".backup-stage-") && !strings.HasPrefix(entry.Name(), ".restore-stage-") {
			continue
		}
		if !entry.IsDir() {
			continue
		}
		directory := filepath.Join(parent, entry.Name())
		if _, err := os.Lstat(filepath.Join(directory, ".amuos-stage")); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := removeStage(parent, directory); err != nil {
			return err
		}
	}
	return nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func Backup(ctx context.Context, database, output, recipientText, version string, config Config) (manifest Manifest, result error) {
	if err := config.validate(); err != nil {
		return manifest, err
	}
	if version == "" {
		return manifest, errors.New("backup requires a product version")
	}
	recipient, err := age.ParseX25519Recipient(recipientText)
	if err != nil {
		return manifest, errors.New("backup requires an age X25519 public key")
	}
	if err := regular(database); err != nil {
		return manifest, err
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return manifest, errors.New("backup destination must not exist")
	}
	lock, err := statelock.Acquire(database)
	if err != nil {
		return manifest, err
	}
	defer lock.Close()
	if err := CleanupStages(database); err != nil {
		return manifest, err
	}
	db, err := store.Open(database)
	if err != nil {
		return manifest, err
	}
	defer db.Close()
	if err := db.RecoverLocalMaintenance(ctx, time.Now()); err != nil {
		return manifest, err
	}
	id := newID()
	if err := db.StartLocalMaintenance(ctx, id, "backup", time.Now(), map[string]any{"product_version": version}); err != nil {
		return manifest, err
	}
	defer func() {
		if result != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result = errors.Join(result, db.FinishLocalMaintenance(cleanup, id, "failed", time.Now()))
		}
	}()
	directory, cleanup, err := scratch(filepath.Dir(database), ".backup-stage-")
	if err != nil {
		return manifest, err
	}
	defer cleanup()
	snapshot := filepath.Join(directory, "database.sqlite")
	boundary := time.Now().UTC()
	if err := db.Snapshot(ctx, snapshot); err != nil {
		return manifest, err
	}
	if err := store.ValidateSnapshot(ctx, snapshot); err != nil {
		return manifest, err
	}
	description, err := describe(ctx, snapshot)
	if err != nil {
		return manifest, err
	}
	counts, err := db.BackupCounts(ctx)
	if err != nil {
		return manifest, err
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return manifest, err
	}
	keys := []byte(`{"format_version":1,"keys":[]}`)
	manifest = Manifest{FormatVersion: 1, ProductVersion: version, SchemaVersion: store.SchemaVersion(), ConfigVersion: 1, ID: id, Boundary: boundary, Recipient: recipientText, KeyIDs: []string{}, Files: map[string]fileRecord{"database.sqlite": description, "config.json": record(configJSON), "keys.json": record(keys)}}
	manifest.Counts = counts
	if err := manifest.validate(version); err != nil {
		return Manifest{}, err
	}
	// An ephemeral second recipient permits a complete read-back verification
	// without requiring the operator's recovery private key at backup time.
	// Its identity remains only in memory and is never written to the archive.
	verification, err := age.GenerateX25519Identity()
	if err != nil {
		return manifest, err
	}
	f, err := os.CreateTemp(filepath.Dir(output), ".amuos-backup-")
	if err != nil {
		return manifest, err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if err := writeArchive(ctx, f, recipient, verification.Recipient(), manifest, snapshot, configJSON, keys); err != nil {
		_ = f.Close()
		return manifest, err
	}
	err = errors.Join(f.Sync(), f.Close())
	if err != nil {
		return manifest, err
	}
	input, err := os.Open(temporary)
	if err != nil {
		return manifest, err
	}
	_, _, err = readArchive(ctx, input, verification, version, "")
	err = errors.Join(err, input.Close())
	if err != nil {
		return manifest, err
	}
	if err := ctx.Err(); err != nil {
		return manifest, err
	}
	// Link publishes the complete verified file atomically without replacing an
	// existing path, including a destination created after our initial check.
	if err := os.Link(temporary, output); err != nil {
		return manifest, err
	}
	if err := syncDirectory(filepath.Dir(output)); err != nil {
		_ = os.Remove(output)
		return manifest, err
	}
	if err := db.FinishLocalMaintenanceDetails(ctx, id, "succeeded", time.Now(), map[string]any{"consistency_boundary": boundary, "product_version": version, "verification": "age_tar_hashes_sqlite", "counts": counts}); err != nil {
		_ = os.Remove(output)
		return manifest, err
	}
	return manifest, nil
}

type RestoreResult struct {
	Backup           Manifest `json:"backup"`
	Config           Config   `json:"config"`
	RetainedDatabase string   `json:"retained_database,omitempty"`
}

func readIdentity(path string) (*age.X25519Identity, error) {
	if err := regular(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("recovery key file must be accessible only by its owner")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxManifest+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxManifest {
		return nil, errors.New("recovery key file exceeds size limit")
	}
	identities, err := age.ParseIdentities(bytes.NewReader(data))
	if err != nil || len(identities) != 1 {
		return nil, errors.New("recovery key file must contain one age X25519 identity")
	}
	identity, ok := identities[0].(*age.X25519Identity)
	if !ok {
		return nil, errors.New("unsupported recovery key type")
	}
	return identity, nil
}

// GenerateRecoveryKey writes the private identity only to a new private file.
// Its public recipient is safe to print and pass to --recipient.
func GenerateRecoveryKey(path string) (string, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return "", err
	}
	f, err := privatefile.Create(path)
	if err != nil {
		return "", err
	}
	_, err = io.WriteString(f, "# AmuOS offline recovery identity\n"+identity.String()+"\n")
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = syncDirectory(filepath.Dir(path))
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return identity.Recipient().String(), nil
}

func Restore(ctx context.Context, database, archive, keyFile, version string) (result RestoreResult, finalErr error) {
	identity, err := readIdentity(keyFile)
	if err != nil {
		return result, err
	}
	if err := regular(archive); err != nil {
		return result, err
	}
	input, err := os.Open(archive)
	if err != nil {
		return result, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return result, err
	}
	if info.Size() > maxArchive {
		return result, errors.New("backup archive exceeds size limit")
	}
	lock, err := statelock.Acquire(database)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	if err := CleanupStages(database); err != nil {
		return result, err
	}
	directory, cleanup, err := scratch(filepath.Dir(database), ".restore-stage-")
	if err != nil {
		return result, err
	}
	defer cleanup()
	manifest, config, err := readArchive(ctx, input, identity, version, directory)
	if err != nil {
		return result, err
	}
	if manifest.Recipient != identity.Recipient().String() {
		return result, errors.New("backup recipient does not match recovery key")
	}
	snapshot := filepath.Join(directory, "database.sqlite")
	if err := store.ValidateSnapshot(ctx, snapshot); err != nil {
		return result, err
	}
	staged, err := store.Open(snapshot)
	if err != nil {
		return result, err
	}
	counts, err := staged.BackupCounts(ctx)
	if err != nil {
		_ = staged.Close()
		return result, err
	}
	if !reflect.DeepEqual(counts, manifest.Counts) {
		_ = staged.Close()
		return result, errors.New("backup record counts do not match database")
	}
	now := time.Now().UTC()
	id := newID()
	details := map[string]any{"backup_id": manifest.ID, "consistency_boundary": manifest.Boundary, "restored_at": now, "product_version": version, "configuration": config, "verification": "age_tar_hashes_sqlite", "audit_gap": "events_after_consistency_boundary_are_not_recovered"}
	err = staged.PrepareRestoredState(ctx, id, now, details)
	err = errors.Join(err, staged.Close())
	if err != nil {
		return result, err
	}
	if err := store.ValidateSnapshot(ctx, snapshot); err != nil {
		return result, err
	}
	if err := syncFile(snapshot); err != nil {
		return result, err
	}
	// The current persisted state is the DB alone. CLI network settings are
	// recorded for review, and server TLS must be provisioned separately.
	existed := false
	if _, err := os.Lstat(database); err == nil {
		existed = true
		if err := regular(database); err != nil {
			return result, err
		}
		if err := store.ValidateSnapshot(ctx, database); err != nil {
			return result, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	db, err := store.Open(database)
	if err != nil {
		return result, err
	}
	defer func() { finalErr = errors.Join(finalErr, db.Close()) }()
	if existed {
		// Retain a private consistent pre-restore DB for rollback/evidence. It is
		// deliberately outside the staging directory and never auto-deleted.
		retained, err := os.MkdirTemp(filepath.Dir(database), ".pre-restore-")
		if err != nil {
			return result, err
		}
		result.RetainedDatabase = filepath.Join(retained, "database.sqlite")
		if err := db.Snapshot(ctx, result.RetainedDatabase); err != nil {
			return result, err
		}
		if err := store.ValidateSnapshot(ctx, result.RetainedDatabase); err != nil {
			return result, err
		}
		if err := syncFile(result.RetainedDatabase); err != nil {
			return result, err
		}
		if err := syncDirectory(retained); err != nil {
			return result, err
		}
		if err := syncDirectory(filepath.Dir(database)); err != nil {
			return result, err
		}
	}
	if err := db.StartLocalMaintenance(ctx, id, "restore", now, details); err != nil {
		return result, err
	}
	defer func() {
		if finalErr != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			finalErr = errors.Join(finalErr, db.FinishLocalMaintenance(cleanup, id, "failed", time.Now()))
		}
	}()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := db.ReplaceFrom(ctx, snapshot); err != nil {
		return result, err
	}
	// Restored credentials are already revoked; removing the CA also makes a
	// new one sign every future credential (specification §14.5).
	if err := agentca.Remove(filepath.Dir(database)); err != nil {
		return result, fmt.Errorf("database restored, but remove the agent CA files before starting: %w", err)
	}
	result.Backup, result.Config = manifest, config
	return result, nil
}
