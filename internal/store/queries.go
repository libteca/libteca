package store

import (
	"database/sql"
	"errors"
	"os"
	"strings"
)

var ErrNotFound = errors.New("not found")

type dbtx interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func workSearchCols(title string, author *string) (string, string) {
	a := ""
	if author != nil {
		a = *author
	}
	return strings.ToLower(title), strings.ToLower(a)
}

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
		frows, err := tx.Query(`SELECT DISTINCT file_id FROM podcast_episodes
			WHERE podcast_id IN (`+pods+`) AND file_id IS NOT NULL`, id)
		if err != nil {
			return err
		}
		var fileIDs []int64
		for frows.Next() {
			var fid int64
			if err := frows.Scan(&fid); err != nil {
				frows.Close()
				return err
			}
			fileIDs = append(fileIDs, fid)
		}
		frows.Close()
		if err := frows.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM podcast_episodes WHERE podcast_id IN (`+pods+`)`, id); err != nil {
			return err
		}
		for _, fid := range fileIDs {
			if _, err := tx.Exec(`DELETE FROM files WHERE id = ? AND edition_id IS NULL
				AND NOT EXISTS (SELECT 1 FROM podcast_episodes WHERE file_id = files.id)`, fid); err != nil {
				return err
			}
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
	err := d.Update(func(tx *Tx) error {
		var ierr error
		id, ierr = upsertWork(tx, w)
		return ierr
	})
	return id, err
}

func (t *Tx) UpsertWork(w *Work) (int64, error) {
	return upsertWork(t, w)
}

func upsertWork(q dbtx, w *Work) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM works WHERE library_id = ? AND lower(title) = lower(?) AND lower(coalesce(author,'')) = lower(coalesce(?,''))`,
		w.LibraryID, w.Title, w.Author).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		now := nowMilli()
		titleL, authorL := workSearchCols(w.Title, w.Author)
		res, ierr := q.Exec(`INSERT INTO works (library_id, title, title_l, subtitle, author, author_l, description, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			w.LibraryID, w.Title, titleL, w.Subtitle, w.Author, authorL, w.Description, now, now)
		if ierr != nil {
			return 0, ierr
		}
		w.Created = true
		return res.LastInsertId()
	}
	if err != nil {
		return 0, err
	}
	titleL, authorL := workSearchCols(w.Title, w.Author)
	_, err = q.Exec(`UPDATE works SET title = ?, title_l = ?, subtitle = ?, author = ?, author_l = ?, description = coalesce(?, works.description), updated_at = ? WHERE id = ?`,
		w.Title, titleL, w.Subtitle, w.Author, authorL, w.Description, nowMilli(), id)
	return id, err
}

func (d *DB) UpsertEdition(e *Edition) (int64, error) {
	var id int64
	err := d.Update(func(tx *Tx) error {
		var ierr error
		id, ierr = upsertEdition(tx, e)
		return ierr
	})
	return id, err
}

func (t *Tx) UpsertEdition(e *Edition) (int64, error) {
	return upsertEdition(t, e)
}

func upsertEdition(q dbtx, e *Edition) (int64, error) {
	var id int64
	var err error
	if e.SeasonNum != nil && e.EpisodeNum != nil {
		err = q.QueryRow(`SELECT id FROM editions WHERE work_id = ? AND season_num = ? AND episode_num = ?`,
			e.WorkID, *e.SeasonNum, *e.EpisodeNum).Scan(&id)
	} else {
		err = q.QueryRow(`SELECT id FROM editions WHERE work_id = ? AND format = ? AND lower(title) = lower(?) AND season_num IS NULL`,
			e.WorkID, e.Format, e.Title).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		res, ierr := q.Exec(`INSERT INTO editions (work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			e.WorkID, e.Format, e.Title, e.Language, e.Abridged, e.DurationSecs, e.Position, e.SeasonNum, e.EpisodeNum, nowMilli())
		if ierr != nil {
			return 0, ierr
		}
		return res.LastInsertId()
	}
	if err != nil {
		return 0, err
	}
	_, err = q.Exec(`UPDATE editions SET language = ?, abridged = ?, duration_secs = ? WHERE id = ?`,
		e.Language, e.Abridged, e.DurationSecs, id)
	return id, err
}

// UpsertFile upserts a file; f.Inserted reports whether a new row was inserted.
// A new path with a known hash re-links the existing row when the old path is
// marked missing or gone from disk (move/rename). Live copies insert a new row.
func (d *DB) UpsertFile(f *FileRec) error {
	return d.Update(func(tx *Tx) error {
		return upsertFile(tx, f)
	})
}

func (t *Tx) UpsertFile(f *FileRec) error {
	return upsertFile(t, f)
}

func upsertFile(q dbtx, f *FileRec) error {
	var id int64
	err := q.QueryRow(`SELECT id FROM files WHERE path = ?`, f.Path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		if f.Hash != nil && *f.Hash != "" {
			relinked, rerr := relinkFile(q, *f.Hash, f)
			if rerr != nil {
				return rerr
			}
			if relinked {
				return nil
			}
		}
		res, ierr := q.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, hash, codec, video_codec, width, height, container, bitrate, channels, sample_rate, duration_secs, chapters, embedded_meta, missing, probed_at)
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
	return updateFileRow(q, id, f)
}

func relinkFile(q dbtx, hash string, f *FileRec) (bool, error) {
	hid, ok, err := relinkableFileID(q, hash, f.Path)
	if err != nil || !ok {
		return false, err
	}
	if err := updateFileRow(q, hid, f); err != nil {
		return false, err
	}
	return true, nil
}

func relinkableFileID(q dbtx, hash, newPath string) (int64, bool, error) {
	rows, err := q.Query(`SELECT id, path, missing FROM files WHERE hash = ? AND path != ? ORDER BY missing DESC, id ASC`, hash, newPath)
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

func updateFileRow(q dbtx, id int64, f *FileRec) error {
	_, err := q.Exec(`UPDATE files SET edition_id = ?, path = ?, seq = ?, size_bytes = ?, mtime_secs = ?, hash = ?, codec = ?, video_codec = ?, width = ?, height = ?, container = ?, bitrate = ?, channels = ?, sample_rate = ?, duration_secs = ?, chapters = ?, missing = 0, probed_at = ? WHERE id = ?`,
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

func (t *Tx) SetWorkCover(workID int64, rel string) error {
	_, err := t.Exec(`UPDATE works SET cover_path = ?, updated_at = ? WHERE id = ? AND (cover_path IS NULL OR cover_path = '')`,
		rel, nowMilli(), workID)
	return err
}
