package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// UpdateCheck reports whether this binary can start with an existing database.
type UpdateCheck struct {
	DatabaseSchema    int  `json:"database_schema"`
	BinarySchema      int  `json:"binary_schema"`
	MigrationRequired bool `json:"migration_required"`
}

func openReadOnly(path string) (*sql.DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{"mode": {"ro"}}
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// CheckUpdate inspects an existing database without creating, migrating, or
// writing to it. A newer schema or a failed integrity check is an error.
func CheckUpdate(ctx context.Context, path string) (UpdateCheck, error) {
	result := UpdateCheck{BinarySchema: SchemaVersion()}
	info, err := os.Lstat(path)
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() {
		return result, errors.New("database must be a regular file")
	}
	db, err := openReadOnly(path)
	if err != nil {
		return result, err
	}
	defer db.Close()
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&result.DatabaseSchema); err != nil {
		return result, err
	}
	if result.DatabaseSchema < 1 {
		return result, errors.New("database is not initialized by this product")
	}
	if result.DatabaseSchema > result.BinarySchema {
		return result, errors.New("database schema is newer than this binary")
	}
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		return result, err
	}
	if check != "ok" {
		return result, errors.New("database integrity check failed")
	}
	result.MigrationRequired = result.DatabaseSchema < result.BinarySchema
	return result, nil
}
