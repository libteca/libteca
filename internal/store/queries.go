package store

import (
	"database/sql"
	"errors"
	"os"
)

var ErrNotFound = errors.New("not found")

func (d *DB) Libraries() ([]Library, error) {
	rows, err := d.Query(`SELECT id, name, type, path, created_at FROM libraries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Name, &l.Type, &l.Path, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (d *DB) Library(id int64) (*Library, error) {
	var l Library
	err := d.QueryRow(`SELECT id, name, type, path, created_at FROM libraries WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Type, &l.Path, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &l, err
}

func (d *DB) AddLibrary(name, typ, path string) (int64, error) {
	res, err := d.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES (?,?,?,?)`,
		name, typ, path, nowMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) DeleteLibrary(id int64) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var typ string
	err = tx.QueryRow(`SELECT type FROM libraries WHERE id = ?`, id).Scan(&typ)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	editions := `SELECT id FROM editions WHERE work_id IN (SELECT id FROM works WHERE library_id = ?)`
	if _, err := tx.Exec(`DELETE FROM progress WHERE edition_id IN (`+editions+`)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM playback_sessions WHERE edition_id IN (`+editions+`)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM playlist_items WHERE edition_id IN (`+editions+`)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM files WHERE edition_id IN (`+editions+`)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM editions WHERE work_id IN (SELECT id FROM works WHERE library_id = ?)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM works WHERE library_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM scan_jobs WHERE library_id = ?`, id); err != nil {
		return err
	}
	if typ == "podcasts" {
		pods := `SELECT id FROM podcasts WHERE library_id = ?`
		eps := `SELECT id FROM podcast_episodes WHERE podcast_id IN (` + pods + `)`
		if _, err := tx.Exec(`DELETE FROM podcast_episode_progress WHERE episode_id IN (`+eps+`)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM podcast_episodes WHERE podcast_id IN (`+pods+`)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM podcasts WHERE library_id = ?`, id); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`DELETE FROM libraries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (d *DB) Users() ([]User, error) {
	rows, err := d.Query(`SELECT id, name, password_hash, is_admin, created_at, updated_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (d *DB) UserByName(name string) (*User, error) {
	var u User
	err := d.QueryRow(`SELECT id, name, password_hash, is_admin, created_at, updated_at FROM users WHERE name = ? COLLATE NOCASE`, name).
		Scan(&u.ID, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

func (d *DB) User(id int64) (*User, error) {
	var u User
	err := d.QueryRow(`SELECT id, name, password_hash, is_admin, created_at, updated_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// UpsertWork upserts a work; w.Created reports whether a new row was inserted.
func (d *DB) UpsertWork(w *Work) (int64, error) {
	var id int64
	err := d.QueryRow(`SELECT id FROM works WHERE library_id = ? AND lower(title) = lower(?) AND lower(coalesce(author,'')) = lower(coalesce(?,''))`,
		w.LibraryID, w.Title, w.Author).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		now := nowMilli()
		res, ierr := d.Exec(`INSERT INTO works (library_id, title, subtitle, author, description, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
			w.LibraryID, w.Title, w.Subtitle, w.Author, w.Description, now, now)
		if ierr != nil {
			return 0, ierr
		}
		w.Created = true
		return res.LastInsertId()
	}
	if err != nil {
		return 0, err
	}
	_, err = d.Exec(`UPDATE works SET title = ?, subtitle = ?, author = ?, description = coalesce(?, works.description), updated_at = ? WHERE id = ?`,
		w.Title, w.Subtitle, w.Author, w.Description, nowMilli(), id)
	return id, err
}

func (d *DB) UpsertEdition(e *Edition) (int64, error) {
	var id int64
	var err error
	if e.SeasonNum != nil && e.EpisodeNum != nil {
		err = d.QueryRow(`SELECT id FROM editions WHERE work_id = ? AND season_num = ? AND episode_num = ?`,
			e.WorkID, *e.SeasonNum, *e.EpisodeNum).Scan(&id)
	} else {
		err = d.QueryRow(`SELECT id FROM editions WHERE work_id = ? AND format = ? AND lower(title) = lower(?) AND season_num IS NULL`,
			e.WorkID, e.Format, e.Title).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		res, ierr := d.Exec(`INSERT INTO editions (work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			e.WorkID, e.Format, e.Title, e.Language, e.Abridged, e.DurationSecs, e.Position, e.SeasonNum, e.EpisodeNum, nowMilli())
		if ierr != nil {
			return 0, ierr
		}
		return res.LastInsertId()
	}
	if err != nil {
		return 0, err
	}
	_, err = d.Exec(`UPDATE editions SET language = ?, abridged = ?, duration_secs = ? WHERE id = ?`,
		e.Language, e.Abridged, e.DurationSecs, id)
	return id, err
}

// UpsertFile upserts a file; f.Inserted reports whether a new row was inserted.
// A new path with a known hash re-links the existing row when the old path is
// marked missing or gone from disk (move/rename). Live copies insert a new row.
func (d *DB) UpsertFile(f *FileRec) error {
	var id int64
	err := d.QueryRow(`SELECT id FROM files WHERE path = ?`, f.Path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		if f.Hash != nil && *f.Hash != "" {
			if hid, ok, herr := d.relinkableFileID(*f.Hash, f.Path); herr != nil {
				return herr
			} else if ok {
				return d.updateFileRow(hid, f)
			}
		}
		res, ierr := d.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, hash, codec, video_codec, width, height, container, bitrate, channels, sample_rate, duration_secs, chapters, embedded_meta, missing, probed_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?)`,
			f.EditionID, f.Path, f.Seq, f.SizeBytes, f.MtimeSecs, f.Hash, f.Codec, f.VideoCodec, f.Width, f.Height, f.Container, f.Bitrate, f.Channels, f.SampleRate, f.DurationSecs, f.Chapters, "{}", nowMilli())
		if ierr != nil {
			return ierr
		}
		fid, _ := res.LastInsertId()
		f.ID = fid
		f.Inserted = true
		return nil
	}
	if err != nil {
		return err
	}
	return d.updateFileRow(id, f)
}

func (d *DB) relinkableFileID(hash, newPath string) (int64, bool, error) {
	rows, err := d.Query(`SELECT id, path, missing FROM files WHERE hash = ? AND path != ? ORDER BY missing DESC, id ASC`, hash, newPath)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var oldPath string
		var missing int
		if err := rows.Scan(&id, &oldPath, &missing); err != nil {
			return 0, false, err
		}
		if missing == 1 {
			return id, true, nil
		}
		if _, err := os.Stat(oldPath); err != nil {
			return id, true, nil
		}
	}
	return 0, false, rows.Err()
}

func (d *DB) updateFileRow(id int64, f *FileRec) error {
	_, err := d.Exec(`UPDATE files SET edition_id = ?, path = ?, seq = ?, size_bytes = ?, mtime_secs = ?, hash = ?, codec = ?, video_codec = ?, width = ?, height = ?, container = ?, bitrate = ?, channels = ?, sample_rate = ?, duration_secs = ?, chapters = ?, missing = 0, probed_at = ? WHERE id = ?`,
		f.EditionID, f.Path, f.Seq, f.SizeBytes, f.MtimeSecs, f.Hash, f.Codec, f.VideoCodec, f.Width, f.Height, f.Container, f.Bitrate, f.Channels, f.SampleRate, f.DurationSecs, f.Chapters, nowMilli(), id)
	f.ID = id
	f.Inserted = false
	return err
}

func (d *DB) SetWorkCover(workID int64, rel string) error {
	_, err := d.Exec(`UPDATE works SET cover_path = ?, updated_at = ? WHERE id = ? AND (cover_path IS NULL OR cover_path = '')`,
		rel, nowMilli(), workID)
	return err
}
