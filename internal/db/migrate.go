package db

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"sort"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		name := entry.Name()

		var applied int
		err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", name).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied > 0 {
			continue
		}

		content, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		if _, err := db.Exec(string(content)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}

		if _, err := db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", name); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}

		slog.Info("applied migration: " + name)
	}

	return nil
}
