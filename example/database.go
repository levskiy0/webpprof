package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/levskiy0/webpprof"
)

var (
	errInvalidPlayerID = errors.New("player id must be a positive integer")
	errPlayerNotFound  = errors.New("player not found")
)

const (
	selectPlayerSQL = `
SELECT id, name, email, views, created_at
FROM players
WHERE id = ?`
	listPlayersSQL = `
SELECT id, name, email, views, created_at
FROM players
ORDER BY id`
)

type player struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Views     int64  `json:"views"`
	CreatedAt string `json:"created_at"`
}

type playerRepository struct {
	database *sql.DB
}

// driverConnector adapts a driver.Driver to database/sql's Connector API so
// the driver can be wrapped before sql.DB owns it. Application repositories use
// an ordinary *sql.DB after this one-time composition-root setup.
type driverConnector struct {
	driver driver.Driver
	dsn    string
}

func (c *driverConnector) Connect(ctx context.Context) (driver.Conn, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		return c.driver.Open(c.dsn)
	}
}

func (c *driverConnector) Driver() driver.Driver {
	return c.driver
}

func openPlayerDatabase(ctx context.Context, databaseDriver driver.Driver, databasePath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	database := sql.OpenDB(&driverConnector{driver: databaseDriver, dsn: databasePath})
	database.SetMaxOpenConns(4)
	database.SetMaxIdleConns(2)
	database.SetConnMaxIdleTime(time.Minute)

	setupCtx := webpprof.WithoutRecording(ctx)
	for _, statement := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		`CREATE TABLE IF NOT EXISTS players (
            id INTEGER PRIMARY KEY,
            name TEXT NOT NULL,
            email TEXT NOT NULL UNIQUE,
            views INTEGER NOT NULL DEFAULT 0,
            created_at TEXT NOT NULL
        )`,
		`INSERT INTO players (id, name, email, views, created_at) VALUES
            (42, 'Ada Lovelace', 'ada@example.test', 7, '2026-08-23T00:00:00Z'),
            (84, 'Grace Hopper', 'grace@example.test', 12, '2026-08-23T00:00:00Z')
        ON CONFLICT (id) DO NOTHING`,
	} {
		if _, err := database.ExecContext(setupCtx, statement); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("initialize example database: %w", err)
		}
	}
	if err := os.Chmod(databasePath, 0o600); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("set database permissions: %w", err)
	}
	return database, nil
}

func (r *playerRepository) find(ctx context.Context, id int64) (player, error) {
	var result player
	err := r.database.QueryRowContext(ctx, selectPlayerSQL, id).Scan(
		&result.ID,
		&result.Name,
		&result.Email,
		&result.Views,
		&result.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return player{}, errPlayerNotFound
	}
	if err != nil {
		return player{}, fmt.Errorf("select player %d: %w", id, err)
	}
	return result, nil
}

func (r *playerRepository) list(ctx context.Context) ([]player, error) {
	rows, err := r.database.QueryContext(ctx, listPlayersSQL)
	if err != nil {
		return nil, fmt.Errorf("select players: %w", err)
	}
	defer rows.Close()

	players := make([]player, 0, 2)
	for rows.Next() {
		var value player
		if err := rows.Scan(&value.ID, &value.Name, &value.Email, &value.Views, &value.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan player: %w", err)
		}
		players = append(players, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate players: %w", err)
	}
	return players, nil
}

func (r *playerRepository) incrementViews(ctx context.Context, id int64) (player, error) {
	transaction, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return player{}, fmt.Errorf("begin increment views: %w", err)
	}
	defer transaction.Rollback()

	result, err := transaction.ExecContext(ctx, `UPDATE players SET views = views + 1 WHERE id = ?`, id)
	if err != nil {
		return player{}, fmt.Errorf("increment player %d views: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return player{}, fmt.Errorf("read incremented rows: %w", err)
	}
	if affected == 0 {
		return player{}, errPlayerNotFound
	}

	var updated player
	if err := transaction.QueryRowContext(ctx, selectPlayerSQL, id).Scan(
		&updated.ID,
		&updated.Name,
		&updated.Email,
		&updated.Views,
		&updated.CreatedAt,
	); err != nil {
		return player{}, fmt.Errorf("select updated player %d: %w", id, err)
	}
	if err := transaction.Commit(); err != nil {
		return player{}, fmt.Errorf("commit increment views: %w", err)
	}
	return updated, nil
}

func (r *playerRepository) forceFailure(ctx context.Context) error {
	var id int64
	if err := r.database.QueryRowContext(ctx, `SELECT id FROM missing_players LIMIT 1`).Scan(&id); err != nil {
		return fmt.Errorf("run deliberate failing query: %w", err)
	}
	return nil
}
