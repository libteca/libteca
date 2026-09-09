package store

import (
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	*sql.DB
}

func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, err
	}
	if err := goose.Up(sdb, "migrations"); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{sdb}, nil
}

func now() int64 { return time.Now().UnixMilli() }
