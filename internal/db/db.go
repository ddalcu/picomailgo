package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB holds separate writer and reader connection pools for SQLite.
type DB struct {
	Writer *sql.DB
	Reader *sql.DB
}

// Open creates (or opens) the SQLite database at path,
// configures WAL mode, and runs pending migrations.
func Open(path string) (*DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	// Writer: single connection to serialize writes
	writer, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	writer.SetMaxOpenConns(1)

	// Reader: multiple connections for concurrent reads
	reader, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&mode=ro")
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}
	reader.SetMaxOpenConns(4)

	if err := migrate(writer); err != nil {
		writer.Close()
		reader.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &DB{Writer: writer, Reader: reader}, nil
}

// Close shuts down both connection pools.
func (d *DB) Close() error {
	rErr := d.Reader.Close()
	wErr := d.Writer.Close()
	if wErr != nil {
		return wErr
	}
	return rErr
}
