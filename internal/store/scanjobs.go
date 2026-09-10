package store

import (
	"database/sql"
	"errors"
)

type ScanJob struct {
	ID           int64
	LibraryID    int64
	Status       string
	Error        *string
	FilesSeen    int64
	FilesProbed  int64
	FilesAdded   int64
	FilesUpdated int64
	WorksChanged int64
	StartedAt    int64
	FinishedAt   *int64
	CreatedAt    int64
}

const scanJobCols = `id, library_id, status, error, files_seen, files_probed, files_added, files_updated, works_changed, started_at, finished_at, created_at`

func scanScanJob(row interface{ Scan(...any) error }) (*ScanJob, error) {
	var j ScanJob
	err := row.Scan(&j.ID, &j.LibraryID, &j.Status, &j.Error, &j.FilesSeen, &j.FilesProbed,
		&j.FilesAdded, &j.FilesUpdated, &j.WorksChanged, &j.StartedAt, &j.FinishedAt, &j.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (d *DB) CreateScanJob(libraryID int64) (int64, error) {
	now := nowMilli()
	res, err := d.Exec(`INSERT INTO scan_jobs (library_id, status, started_at, created_at) VALUES (?,?,?,?)`,
		libraryID, "running", now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) UpdateScanJobCounts(id, seen, probed, added, updated, works int64) error {
	_, err := d.Exec(`UPDATE scan_jobs SET files_seen = ?, files_probed = ?, files_added = ?, files_updated = ?, works_changed = ? WHERE id = ?`,
		seen, probed, added, updated, works, id)
	return err
}

func (d *DB) FinishScanJob(id int64, status string, errMsg *string) error {
	_, err := d.Exec(`UPDATE scan_jobs SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, errMsg, nowMilli(), id)
	return err
}

func (d *DB) ListScanJobs(libraryID int64, limit int) ([]ScanJob, error) {
	rows, err := d.Query(`SELECT `+scanJobCols+` FROM scan_jobs WHERE library_id = ? ORDER BY id DESC LIMIT ?`, libraryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanJob
	for rows.Next() {
		j, err := scanScanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (d *DB) GetScanJob(id int64) (*ScanJob, error) {
	row := d.QueryRow(`SELECT `+scanJobCols+` FROM scan_jobs WHERE id = ?`, id)
	j, err := scanScanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (d *DB) FailRunningScanJobs() (int64, error) {
	res, err := d.Exec(`UPDATE scan_jobs SET status = 'error', error = 'interrupted', finished_at = ? WHERE status = 'running'`, nowMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
