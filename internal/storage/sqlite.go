package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"net/url"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schema string

// Open opens a file-backed database and initializes its schema.
// Driver options apply to every connection created by the pool.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	options := url.Values{
		"_txlock":       {"immediate"},
		"_foreign_keys": {"on"},
		"_busy_timeout": {"5000"},
		"_journal_mode": {"WAL"},
		"_synchronous":  {"FULL"},
	}
	dsn := (&url.URL{Scheme: "file", Path: absolutePath, RawQuery: options.Encode()}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}
	return db, nil
}
