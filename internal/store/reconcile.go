package store

import (
	"errors"
	"io/fs"
	"os"
)

func (d *DB) MarkMissingLibraryFiles(libraryID int64) (int, error) {
	rows, err := d.Query(`SELECT f.id, f.path
		FROM files f
		JOIN editions e ON e.id = f.edition_id
		JOIN works w ON w.id = e.work_id
		WHERE w.library_id = ? AND f.missing = 0`, libraryID)
	if err != nil {
		return 0, err
	}
	var gone []int64
	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			rows.Close()
			return 0, err
		}
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			gone = append(gone, id)
		}
	}
	scanErr := rows.Err()
	closeErr := rows.Close()
	if scanErr != nil {
		return 0, scanErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if len(gone) == 0 {
		return 0, nil
	}
	err = d.Update(func(tx *Tx) error {
		for _, id := range gone {
			if _, err := tx.Exec(`UPDATE files SET missing = 1 WHERE id = ? AND missing = 0`, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(gone), nil
}
