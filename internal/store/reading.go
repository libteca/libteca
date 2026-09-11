package store

import (
	"database/sql"
	"errors"
)

// ReadingProgress extends Progress with the books/comics columns from
// migration 0004. It embeds Progress rather than growing types.go because
// every existing caller of SetProgress/GetProgress stays source-compatible.
type ReadingProgress struct {
	Progress
	Page    *int64
	Percent *float64
	Locator *string
}

func (d *DB) ReadingListByUser(userID int64) (map[int64]*ReadingProgress, error) {
	rows, err := d.Query(`SELECT user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, page, percent, locator
		FROM progress WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*ReadingProgress{}
	for rows.Next() {
		var p ReadingProgress
		var fin int
		if err := rows.Scan(&p.UserID, &p.EditionID, &p.FileID, &p.FileOffsetSecs, &p.EditionPositionSecs, &p.DurationSecs, &fin, &p.Device, &p.UpdatedAt, &p.Page, &p.Percent, &p.Locator); err != nil {
			return nil, err
		}
		p.IsFinished = fin != 0
		out[p.EditionID] = &p
	}
	return out, rows.Err()
}

func (d *DB) GetReadingProgress(userID, editionID int64) (*ReadingProgress, error) {
	var p ReadingProgress
	var fin int
	err := d.QueryRow(`SELECT user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, page, percent, locator
		FROM progress WHERE user_id = ? AND edition_id = ?`, userID, editionID).
		Scan(&p.UserID, &p.EditionID, &p.FileID, &p.FileOffsetSecs, &p.EditionPositionSecs, &p.DurationSecs, &fin, &p.Device, &p.UpdatedAt, &p.Page, &p.Percent, &p.Locator)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.IsFinished = fin != 0
	return &p, nil
}

// SetReadingProgress is SetProgress plus the reading columns. The reading
// columns only overwrite when provided, so an audio-style post to the same
// edition never wipes page state.
func (d *DB) SetReadingProgress(p *ReadingProgress) error {
	_, err := d.Exec(`INSERT INTO progress (user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, page, percent, locator)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(user_id, edition_id) DO UPDATE SET
			file_id = excluded.file_id,
			file_offset_secs = excluded.file_offset_secs,
			edition_position_secs = excluded.edition_position_secs,
			duration_secs = excluded.duration_secs,
			is_finished = excluded.is_finished,
			device = excluded.device,
			updated_at = excluded.updated_at,
			page = coalesce(excluded.page, progress.page),
			percent = coalesce(excluded.percent, progress.percent),
			locator = coalesce(excluded.locator, progress.locator)`,
		p.UserID, p.EditionID, p.FileID, p.FileOffsetSecs, p.EditionPositionSecs, p.DurationSecs, p.IsFinished, p.Device, nowMilli(), p.Page, p.Percent, p.Locator)
	return err
}

// EditionPages is Edition plus page_count; the scanner upserts book editions
// through it because UpsertEdition does not know the column.
type EditionPages struct {
	Edition
	PageCount *int
}

func (d *DB) UpsertEditionPages(e *EditionPages) (int64, error) {
	var id int64
	err := d.Update(func(tx *Tx) error {
		var ierr error
		id, ierr = upsertEditionPages(tx, e)
		return ierr
	})
	return id, err
}

func (t *Tx) UpsertEditionPages(e *EditionPages) (int64, error) {
	return upsertEditionPages(t, e)
}

func upsertEditionPages(q dbtx, e *EditionPages) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM editions WHERE work_id = ? AND format = ? AND lower(title) = lower(?) AND season_num IS NULL`,
		e.WorkID, e.Format, e.Title).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, ierr := q.Exec(`INSERT INTO editions (work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, page_count, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			e.WorkID, e.Format, e.Title, e.Language, e.Abridged, e.DurationSecs, e.Position, e.SeasonNum, e.EpisodeNum, e.PageCount, nowMilli())
		if ierr != nil {
			return 0, ierr
		}
		return res.LastInsertId()
	}
	if err != nil {
		return 0, err
	}
	_, err = q.Exec(`UPDATE editions SET language = ?, abridged = ?, duration_secs = ?, page_count = ? WHERE id = ?`,
		e.Language, e.Abridged, e.DurationSecs, e.PageCount, id)
	return id, err
}

// FindWorkID mirrors the works unique-index lookup used by UpsertWork.
func (d *DB) FindWorkID(libraryID int64, title string, author *string) (int64, bool) {
	return findWorkID(d, libraryID, title, author)
}

func (t *Tx) FindWorkID(libraryID int64, title string, author *string) (int64, bool) {
	return findWorkID(t, libraryID, title, author)
}

func findWorkID(q dbtx, libraryID int64, title string, author *string) (int64, bool) {
	var id int64
	err := q.QueryRow(`SELECT id FROM works WHERE library_id = ? AND lower(title) = lower(?) AND lower(coalesce(author,'')) = lower(coalesce(?,''))`,
		libraryID, title, author).Scan(&id)
	if err != nil {
		return 0, false
	}
	return id, true
}

func (d *DB) PageCountsByWork(workID int64) (map[int64]*int, error) {
	rows, err := d.Query(`SELECT id, page_count FROM editions WHERE work_id = ? AND page_count IS NOT NULL`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*int{}
	for rows.Next() {
		var id int64
		var pc int
		if err := rows.Scan(&id, &pc); err != nil {
			return nil, err
		}
		v := pc
		out[id] = &v
	}
	return out, rows.Err()
}

// EditionFile returns the first file of an edition plus its format, for the
// download endpoint. The path comes from the files table only.
func (d *DB) EditionFile(editionID int64) (*FileRec, string, error) {
	var f FileRec
	var format string
	var missing int
	err := d.QueryRow(`SELECT f.id, f.edition_id, f.path, f.seq, f.size_bytes, f.mtime_secs, f.hash, f.codec, f.video_codec, f.width, f.height, f.container, f.bitrate, f.channels, f.sample_rate, f.duration_secs, f.chapters, f.missing, e.format
		FROM files f JOIN editions e ON e.id = f.edition_id
		WHERE f.edition_id = ? AND f.missing = 0 ORDER BY f.seq LIMIT 1`, editionID).
		Scan(&f.ID, &f.EditionID, &f.Path, &f.Seq, &f.SizeBytes, &f.MtimeSecs, &f.Hash, &f.Codec, &f.VideoCodec, &f.Width, &f.Height, &f.Container, &f.Bitrate, &f.Channels, &f.SampleRate, &f.DurationSecs, &f.Chapters, &missing, &format)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	f.Missing = missing != 0
	return &f, format, nil
}

// FileStatByPath reports the recorded size/mtime so the scanner can skip
// probing unchanged book files.
func (d *DB) FileStatByPath(path string) (int64, int64, bool, error) {
	var size, mtime int64
	err := d.QueryRow(`SELECT size_bytes, mtime_secs FROM files WHERE path = ? AND missing = 0`, path).Scan(&size, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return size, mtime, true, nil
}

func (d *DB) SetFileMeta(fileID int64, meta string) error {
	_, err := d.Exec(`UPDATE files SET embedded_meta = ? WHERE id = ?`, meta, fileID)
	return err
}

func (t *Tx) SetFileMeta(fileID int64, meta string) error {
	_, err := t.Exec(`UPDATE files SET embedded_meta = ? WHERE id = ?`, meta, fileID)
	return err
}
