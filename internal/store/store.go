package store

import (
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"path/filepath"
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
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	q := url.Values{}
	q.Set("_txlock", "immediate")
	for _, pragma := range []string{
		"journal_mode(WAL)", "synchronous(NORMAL)",
		"busy_timeout(5000)", "foreign_keys(1)",
	} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	sdb, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = sdb.Close()
		}
	}()
	sdb.SetMaxOpenConns(16)
	sdb.SetMaxIdleConns(16)
	sdb.SetConnMaxLifetime(0)
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
	success = true
	return d, nil
}

// Update runs fn inside a single transaction (all-or-nothing) and commits
// when fn returns nil. Transactions begin IMMEDIATE via the DSN so
// read-then-write sequences cannot race across goroutines and never hit
// SQLITE_BUSY on a deferred-lock upgrade.
func (d *DB) Update(fn func(tx *Tx) error) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(&Tx{tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// Tx is a store handle bound to one transaction; it carries the write paths
// that callers need to compose atomically. Never retained after Update
// returns.
type Tx struct {
	*sql.Tx
}

func (d *DB) backfillWorkSearch() error {
	return d.Update(func(tx *Tx) error {
		rows, err := tx.Query(`SELECT id, title, author FROM works WHERE title_l IS NULL`)
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
			if _, err := tx.Exec(`UPDATE works SET title_l = ?, author_l = ? WHERE id = ?`, titleL, authorL, p.id); err != nil {
				return err
			}
		}
		return nil
	})
}

func now() int64 { return time.Now().UnixMilli() }
