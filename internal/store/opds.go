package store

import (
	"database/sql"
	"strings"
)

// Read-only queries for the OPDS face (internal/api/opds). One feed entry =
// one edition joined to its work; only epub/pdf/cbz/cbr editions with at least
// one non-missing file are exposed.

type OPDSEdition struct {
	EditionID int64
	Format    string
	PageCount *int
	CreatedAt int64
	WorkID    int64
	LibraryID int64
	Title     string
	Author    *string
	CoverPath *string
	WorkAdded int64
}

const opdsEditionCols = `
	e.id, e.format, e.page_count, e.created_at, w.id, w.library_id, w.title, w.author, w.cover_path, w.created_at`

const opdsEditionFrom = `
	FROM editions e
	JOIN works w ON w.id = e.work_id
	JOIN libraries l ON l.id = w.library_id
	WHERE e.format IN ('epub','pdf','cbz','cbr')
	  AND EXISTS (SELECT 1 FROM files f WHERE f.edition_id = e.id AND f.missing = 0)`

const opdsBookTypes = ` AND l.type IN ('books','comics')`

func scanOPDSEdition(rows *sql.Rows) (OPDSEdition, error) {
	var e OPDSEdition
	err := rows.Scan(&e.EditionID, &e.Format, &e.PageCount, &e.CreatedAt, &e.WorkID,
		&e.LibraryID, &e.Title, &e.Author, &e.CoverPath, &e.WorkAdded)
	return e, err
}

func (d *DB) opdsCount(where string, args ...any) (int, error) {
	var total int
	err := d.QueryRow(`SELECT COUNT(*)`+opdsEditionFrom+where, args...).Scan(&total)
	return total, err
}

func (d *DB) opdsEditions(tail string, args []any, limit, offset int) ([]OPDSEdition, error) {
	rows, err := d.Query(`SELECT `+opdsEditionCols+opdsEditionFrom+tail+` LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OPDSEdition
	for rows.Next() {
		e, err := scanOPDSEdition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// OPDSEditionsInLibrary pages one library's editions, title asc.
func (d *DB) OPDSEditionsInLibrary(libID int64, limit, offset int) ([]OPDSEdition, int, error) {
	const where = ` AND w.library_id = ?`
	total, err := d.opdsCount(where, libID)
	if err != nil {
		return nil, 0, err
	}
	eds, err := d.opdsEditions(where+` ORDER BY lower(w.title) ASC, w.id ASC, e.id ASC`, []any{libID}, limit, offset)
	return eds, total, err
}

// OPDSAllEditions pages every edition across books+comics libraries, title asc.
func (d *DB) OPDSAllEditions(limit, offset int) ([]OPDSEdition, int, error) {
	total, err := d.opdsCount(opdsBookTypes)
	if err != nil {
		return nil, 0, err
	}
	eds, err := d.opdsEditions(opdsBookTypes+` ORDER BY lower(w.title) ASC, w.id ASC, e.id ASC`, nil, limit, offset)
	return eds, total, err
}

// OPDSNewestEditions pages editions by work added date, newest first.
func (d *DB) OPDSNewestEditions(limit, offset int) ([]OPDSEdition, int, error) {
	total, err := d.opdsCount(opdsBookTypes)
	if err != nil {
		return nil, 0, err
	}
	eds, err := d.opdsEditions(opdsBookTypes+` ORDER BY w.created_at DESC, w.id DESC, e.id ASC`, nil, limit, offset)
	return eds, total, err
}

// OPDSEditionsInProgress pages the user's unfinished reading editions
// (page or percent set, is_finished = 0), most recently touched first.
func (d *DB) OPDSEditionsInProgress(userID int64, limit, offset int) ([]OPDSEdition, int, error) {
	const where = ` AND EXISTS (SELECT 1 FROM progress p WHERE p.edition_id = e.id AND p.user_id = ?
		AND p.is_finished = 0 AND (p.page IS NOT NULL OR p.percent IS NOT NULL))`
	order := ` ORDER BY (SELECT p.updated_at FROM progress p WHERE p.edition_id = e.id AND p.user_id = ?
		AND p.is_finished = 0 AND (p.page IS NOT NULL OR p.percent IS NOT NULL)) DESC, e.id DESC`
	total, err := d.opdsCount(where, userID)
	if err != nil {
		return nil, 0, err
	}
	eds, err := d.opdsEditions(where+order, []any{userID, userID}, limit, offset)
	return eds, total, err
}

// OPDSSearchEditions matches q case-insensitively against works title/author
// (same ILIKE pattern as SearchWorks), books+comics only, title matches
// before author-only matches.
func (d *DB) OPDSSearchEditions(q string, limit, offset int) ([]OPDSEdition, int, error) {
	pat := "%" + likeEscape(strings.ToLower(q)) + "%"
	const where = opdsBookTypes + ` AND (lower(w.title) LIKE ? ESCAPE '\' OR (w.author IS NOT NULL AND lower(w.author) LIKE ? ESCAPE '\'))`
	const order = ` ORDER BY (lower(w.title) LIKE ? ESCAPE '\') DESC, lower(w.title) ASC, w.id ASC, e.id ASC`
	total, err := d.opdsCount(where, pat, pat)
	if err != nil {
		return nil, 0, err
	}
	eds, err := d.opdsEditions(where+order, []any{pat, pat, pat}, limit, offset)
	return eds, total, err
}
