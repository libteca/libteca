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
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
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
	d := &DB{sdb}
	if err := d.backfillWorkSearch(); err != nil {
		return nil, fmt.Errorf("backfill works search columns: %w", err)
	}
	return d, nil
}

func (d *DB) backfillWorkSearch() error {
	rows, err := d.Query(`SELECT id, title, author FROM works WHERE title_l IS NULL`)
	if err != nil {
		return err
	}
	type pending struct {
		id     int64
		title  string
		author *string
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.title, &p.author); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range batch {
		titleL, authorL := workSearchCols(p.title, p.author)
		if _, err := d.Exec(`UPDATE works SET title_l = ?, author_l = ? WHERE id = ?`, titleL, authorL, p.id); err != nil {
			return err
		}
	}
	return nil
}

func now() int64 { return time.Now().UnixMilli() }
