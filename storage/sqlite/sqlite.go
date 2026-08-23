// Package sqlite provides optional SQLite persistence for webpprof captures.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/levskiy0/webpprof"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS webpprof_entries (
    id TEXT PRIMARY KEY,
    cursor INTEGER NOT NULL UNIQUE,
    recorded_at INTEGER NOT NULL,
    payload BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS webpprof_entries_cursor_idx
    ON webpprof_entries (cursor);
CREATE TABLE IF NOT EXISTS webpprof_meta (
    key TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);
INSERT INTO webpprof_meta (key, value)
VALUES ('next_cursor', 0)
ON CONFLICT (key) DO NOTHING;
`

// Storage persists a webpprof event window in a SQLite database.
type Storage struct {
	database *sql.DB
}

// Open initializes an owner-only SQLite database at path.
func Open(ctx context.Context, path string) (*Storage, error) {
	if path == "" {
		return nil, errors.New("sqlite storage path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create sqlite storage directory: %w", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite storage: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	storage := &Storage{database: database}
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		schema,
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("initialize sqlite storage: %w", err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("set sqlite storage permissions: %w", err)
	}
	return storage, nil
}

// Name identifies this backend in webpprof storage statistics.
func (*Storage) Name() string { return "sqlite" }

// Load restores entries and the last assigned cursor.
func (s *Storage) Load(ctx context.Context) ([]webpprof.Entry, uint64, error) {
	rows, err := s.database.QueryContext(ctx, `SELECT payload FROM webpprof_entries ORDER BY cursor ASC`)
	if err != nil {
		return nil, 0, fmt.Errorf("load sqlite entries: %w", err)
	}
	defer rows.Close()

	entries := make([]webpprof.Entry, 0)
	var loadErr error
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("scan sqlite entry: %w", err))
			continue
		}
		var entry webpprof.Entry
		if err := json.Unmarshal(payload, &entry); err != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("decode sqlite entry: %w", err))
			continue
		}
		if entry.ID != "" {
			entries = append(entries, entry)
		}
	}
	if err := rows.Err(); err != nil {
		loadErr = errors.Join(loadErr, fmt.Errorf("iterate sqlite entries: %w", err))
	}

	var nextCursor int64
	if err := s.database.QueryRowContext(ctx, `SELECT value FROM webpprof_meta WHERE key = 'next_cursor'`).Scan(&nextCursor); err != nil {
		loadErr = errors.Join(loadErr, fmt.Errorf("load sqlite cursor: %w", err))
	}
	if nextCursor < 0 {
		loadErr = errors.Join(loadErr, errors.New("load sqlite cursor: negative value"))
		nextCursor = 0
	}
	return entries, uint64(nextCursor), loadErr
}

// Put inserts or updates one entry and persists the monotonic cursor.
func (s *Storage) Put(ctx context.Context, entry webpprof.Entry, nextCursor uint64) error {
	if nextCursor > math.MaxInt64 || entry.Cursor > math.MaxInt64 {
		return errors.New("persist sqlite entry: cursor exceeds sqlite integer range")
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode sqlite entry: %w", err)
	}
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite entry transaction: %w", err)
	}
	defer transaction.Rollback()

	if _, err := transaction.ExecContext(ctx, `
INSERT INTO webpprof_entries (id, cursor, recorded_at, payload)
VALUES (?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    cursor = excluded.cursor,
    recorded_at = excluded.recorded_at,
    payload = excluded.payload`, entry.ID, int64(entry.Cursor), entry.RecordedAt.UnixNano(), payload); err != nil {
		return fmt.Errorf("persist sqlite entry: %w", err)
	}
	if err := persistCursor(ctx, transaction, nextCursor); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit sqlite entry: %w", err)
	}
	return nil
}

// Delete removes one retained entry.
func (s *Storage) Delete(ctx context.Context, id string) error {
	if _, err := s.database.ExecContext(ctx, `DELETE FROM webpprof_entries WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete sqlite entry: %w", err)
	}
	return nil
}

// Clear removes all entries while preserving the monotonic cursor.
func (s *Storage) Clear(ctx context.Context, nextCursor uint64) error {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite clear transaction: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `DELETE FROM webpprof_entries`); err != nil {
		return fmt.Errorf("clear sqlite entries: %w", err)
	}
	if err := persistCursor(ctx, transaction, nextCursor); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit sqlite clear: %w", err)
	}
	return nil
}

func persistCursor(ctx context.Context, transaction *sql.Tx, nextCursor uint64) error {
	if nextCursor > math.MaxInt64 {
		return errors.New("persist sqlite cursor: value exceeds sqlite integer range")
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO webpprof_meta (key, value)
VALUES ('next_cursor', ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, int64(nextCursor)); err != nil {
		return fmt.Errorf("persist sqlite cursor: %w", err)
	}
	return nil
}

// Close releases the SQLite connection.
func (s *Storage) Close() error {
	if s == nil || s.database == nil {
		return nil
	}
	return s.database.Close()
}

var _ webpprof.EntryStorage = (*Storage)(nil)
