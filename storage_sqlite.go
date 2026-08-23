package webpprof

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteStorageSchema = `
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

type sqliteEntryStorage struct {
	database *sql.DB
}

func (s *entryStore) openSQLiteStorage() {
	storage, err := newSQLiteEntryStorage(s.storagePath)
	if err != nil {
		s.storageError = err.Error()
		return
	}
	s.storageSQLite = storage

	entries, cursor, err := storage.load()
	for _, entry := range entries {
		copy := entry
		s.replayRecord(storeJournalRecord{Operation: "put", Entry: &copy})
	}
	if cursor > s.nextCursor {
		s.nextCursor = cursor
	}
	if err != nil {
		s.storageError = err.Error()
	}

	// The persisted database is authoritative across restarts, but the active
	// in-memory window still owns retention and capacity. Prune rows that no
	// longer belong to that window while the store lock is held by its caller.
	s.purgeExpiredLocked(time.Now())
	s.evictLocked()
}

func newSQLiteEntryStorage(storagePath string) (*sqliteEntryStorage, error) {
	if err := os.MkdirAll(filepath.Dir(storagePath), 0o700); err != nil {
		return nil, fmt.Errorf("create sqlite storage directory: %w", err)
	}
	database, err := sql.Open("sqlite", storagePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite storage: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	storage := &sqliteEntryStorage{database: database}
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		sqliteStorageSchema,
	} {
		if _, err := database.Exec(statement); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("initialize sqlite storage: %w", err)
		}
	}
	if err := os.Chmod(storagePath, 0o600); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("set sqlite storage permissions: %w", err)
	}
	return storage, nil
}

func (s *sqliteEntryStorage) load() ([]Entry, uint64, error) {
	rows, err := s.database.Query(`SELECT payload FROM webpprof_entries ORDER BY cursor ASC`)
	if err != nil {
		return nil, 0, fmt.Errorf("load sqlite entries: %w", err)
	}

	entries := make([]Entry, 0)
	var loadErr error
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			loadErr = errors.Join(loadErr, fmt.Errorf("scan sqlite entry: %w", err))
			continue
		}
		var entry Entry
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
	if err := rows.Close(); err != nil {
		loadErr = errors.Join(loadErr, fmt.Errorf("close sqlite entries: %w", err))
	}

	var nextCursor int64
	if err := s.database.QueryRow(`SELECT value FROM webpprof_meta WHERE key = 'next_cursor'`).Scan(&nextCursor); err != nil {
		loadErr = errors.Join(loadErr, fmt.Errorf("load sqlite cursor: %w", err))
	}
	if nextCursor < 0 {
		loadErr = errors.Join(loadErr, errors.New("load sqlite cursor: negative value"))
		nextCursor = 0
	}
	return entries, uint64(nextCursor), loadErr
}

func (s *sqliteEntryStorage) put(entry Entry, nextCursor uint64) error {
	if nextCursor > math.MaxInt64 || entry.Cursor > math.MaxInt64 {
		return errors.New("persist sqlite entry: cursor exceeds sqlite integer range")
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode sqlite entry: %w", err)
	}
	transaction, err := s.database.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite entry transaction: %w", err)
	}
	defer transaction.Rollback()

	if _, err := transaction.Exec(`
INSERT INTO webpprof_entries (id, cursor, recorded_at, payload)
VALUES (?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    cursor = excluded.cursor,
    recorded_at = excluded.recorded_at,
    payload = excluded.payload`, entry.ID, int64(entry.Cursor), entry.RecordedAt.UnixNano(), payload); err != nil {
		return fmt.Errorf("persist sqlite entry: %w", err)
	}
	if err := persistSQLiteCursor(transaction, nextCursor); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit sqlite entry: %w", err)
	}
	return nil
}

func (s *sqliteEntryStorage) delete(id string) error {
	if _, err := s.database.Exec(`DELETE FROM webpprof_entries WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete sqlite entry: %w", err)
	}
	return nil
}

func (s *sqliteEntryStorage) clear(nextCursor uint64) error {
	transaction, err := s.database.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite clear transaction: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec(`DELETE FROM webpprof_entries`); err != nil {
		return fmt.Errorf("clear sqlite entries: %w", err)
	}
	if err := persistSQLiteCursor(transaction, nextCursor); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit sqlite clear: %w", err)
	}
	return nil
}

func persistSQLiteCursor(transaction *sql.Tx, nextCursor uint64) error {
	if nextCursor > math.MaxInt64 {
		return errors.New("persist sqlite cursor: value exceeds sqlite integer range")
	}
	if _, err := transaction.Exec(`
INSERT INTO webpprof_meta (key, value)
VALUES ('next_cursor', ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`, int64(nextCursor)); err != nil {
		return fmt.Errorf("persist sqlite cursor: %w", err)
	}
	return nil
}

func (s *sqliteEntryStorage) close() error {
	if s == nil || s.database == nil {
		return nil
	}
	return s.database.Close()
}
