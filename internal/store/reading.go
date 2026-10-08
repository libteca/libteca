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
	rows, err := d.Query(`SELECT user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, page, percent, locator, revision
		FROM progress WHERE user_id = ? AND deleted = 0`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*ReadingProgress{}
	for rows.Next() {
		var p ReadingProgress
		var fin int
		if err := rows.Scan(&p.UserID, &p.EditionID, &p.FileID, &p.FileOffsetSecs, &p.EditionPositionSecs, &p.DurationSecs, &fin, &p.Device, &p.UpdatedAt, &p.Page, &p.Percent, &p.Locator, &p.Revision); err != nil {
			return nil, err
		}
		p.IsFinished = fin != 0
		out[p.EditionID] = &p
	}
	return out, rows.Err()
}

func (d *DB) GetReadingProgress(userID, editionID int64) (*ReadingProgress, error) {
	var p ReadingProgress
	var fin, deleted int
	err := d.QueryRow(`SELECT user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, page, percent, locator, revision, reset_generation, deleted
		FROM progress WHERE user_id = ? AND edition_id = ?`, userID, editionID).
		Scan(&p.UserID, &p.EditionID, &p.FileID, &p.FileOffsetSecs, &p.EditionPositionSecs, &p.DurationSecs, &fin, &p.Device, &p.UpdatedAt, &p.Page, &p.Percent, &p.Locator, &p.Revision, &p.ResetGeneration, &deleted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.IsFinished = fin != 0
	p.Deleted = deleted != 0
	return &p, nil
}

// SetReadingProgress is the full-state writer: every column it carries is
// applied, unlike SetReadingProgressPatch which preserves omitted audio
// fields.
func (d *DB) SetReadingProgress(p *ReadingProgress) error {
	return d.SetReadingProgressFields(p, ProgressFields{
		Position: true,
		Duration: true,
		Device:   true,
		Finished: true,
	})
}

func (t *Tx) SetReadingProgress(p *ReadingProgress) error {
	return t.SetReadingProgressFields(p, ProgressFields{
		Position: true,
		Duration: true,
		Device:   true,
		Finished: true,
	})
}

func (d *DB) SetReadingProgressPatch(p *ReadingProgress, finishedProvided bool) error {
	return d.SetReadingProgressFields(p, ProgressFields{Finished: finishedProvided})
}

type ProgressFields struct {
	Position bool
	Duration bool
	Device   bool
	Finished bool
}

func (d *DB) SetReadingProgressFields(p *ReadingProgress, fields ProgressFields) error {
	return setReadingProgressFields(d, p, fields)
}

func (t *Tx) SetReadingProgressFields(p *ReadingProgress, fields ProgressFields) error {
	return setReadingProgressFields(t, p, fields)
}

func setReadingProgressFields(q dbtx, p *ReadingProgress, fields ProgressFields) error {
	_, err := q.Exec(`INSERT INTO progress (user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, page, percent, locator, revision)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,1)
		ON CONFLICT(user_id, edition_id) DO UPDATE SET
			file_id = CASE WHEN ? THEN excluded.file_id ELSE progress.file_id END,
			file_offset_secs = CASE WHEN ? THEN excluded.file_offset_secs ELSE progress.file_offset_secs END,
			edition_position_secs = CASE WHEN ? THEN excluded.edition_position_secs ELSE progress.edition_position_secs END,
			duration_secs = CASE WHEN ? THEN excluded.duration_secs ELSE progress.duration_secs END,
			is_finished = CASE WHEN ? THEN excluded.is_finished ELSE progress.is_finished END,
			device = CASE WHEN ? THEN excluded.device ELSE progress.device END,
			updated_at = excluded.updated_at,
			page = coalesce(excluded.page, progress.page),
			percent = coalesce(excluded.percent, progress.percent),
			locator = coalesce(excluded.locator, progress.locator),
			deleted = 0,
			revision = progress.revision + 1`,
		p.UserID, p.EditionID, p.FileID, p.FileOffsetSecs, p.EditionPositionSecs, p.DurationSecs, p.IsFinished, p.Device, nowMilli(), p.Page, p.Percent, p.Locator,
		fields.Position, fields.Position, fields.Position, fields.Duration, fields.Finished, fields.Device)
	return err
}

// SetReadingProgressRevision applies p only when the stored row matches the
// caller's view of it. baseRevision 0 means "no row exists" and only ever
// inserts; a positive base is a conditional UPDATE and never resurrects a
// row (an absent row stays absent), so a deleted-then-recreated lineage can
// never collide with a base captured before the delete. baseGeneration < 0
// means the caller is not generation-aware and only the revision is
// compared (the documented compatibility scope); a presented generation
// must additionally match the row's reset generation, so an operation
// captured before a reset is rejected even if a later write gave the row a
// familiar revision. applied is false when the row moved underneath the
// caller; the caller re-reads and answers the stale base with the current
// state. Ordinary writes never change the reset generation: it is retained
// through tombstone clears and row recreation and advances only on
// DeleteProgress.
func (d *DB) SetReadingProgressRevision(p *ReadingProgress, fields ProgressFields, baseRevision, baseGeneration int64) (int64, int64, bool, error) {
	return d.SetReadingProgressTimelineRevision(p, fields, baseRevision, baseGeneration, -1)
}

func (d *DB) SetReadingProgressTimelineRevision(p *ReadingProgress, fields ProgressFields, baseRevision, baseGeneration, timelineGeneration int64) (int64, int64, bool, error) {
	var revision, generation int64
	var err error
	if baseRevision == 0 {
		err = d.QueryRow(`INSERT INTO progress (user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at, page, percent, locator, revision)
			SELECT ?,?,?,?,?,?,?,?,?,?,?,?,1 WHERE EXISTS (SELECT 1 FROM editions WHERE id=? AND (?<0 OR timeline_generation=?))
			ON CONFLICT(user_id, edition_id) DO NOTHING
			RETURNING revision, reset_generation`,
			p.UserID, p.EditionID, p.FileID, p.FileOffsetSecs, p.EditionPositionSecs, p.DurationSecs, p.IsFinished, p.Device, nowMilli(), p.Page, p.Percent, p.Locator, p.EditionID, timelineGeneration, timelineGeneration).Scan(&revision, &generation)
	} else {
		query := `UPDATE progress SET
			file_id = CASE WHEN ? THEN ? ELSE progress.file_id END,
			file_offset_secs = CASE WHEN ? THEN ? ELSE progress.file_offset_secs END,
			edition_position_secs = CASE WHEN ? THEN ? ELSE progress.edition_position_secs END,
			duration_secs = CASE WHEN ? THEN ? ELSE progress.duration_secs END,
			is_finished = CASE WHEN ? THEN ? ELSE progress.is_finished END,
			device = CASE WHEN ? THEN ? ELSE progress.device END,
			updated_at = ?,
			page = coalesce(?, progress.page),
			percent = coalesce(?, progress.percent),
			locator = coalesce(?, progress.locator),
			deleted = 0,
			revision = progress.revision + 1
		WHERE user_id = ? AND edition_id = ? AND revision = ?`
		args := []any{
			fields.Position, p.FileID,
			fields.Position, p.FileOffsetSecs,
			fields.Position, p.EditionPositionSecs,
			fields.Duration, p.DurationSecs,
			fields.Finished, p.IsFinished,
			fields.Device, p.Device,
			nowMilli(), p.Page, p.Percent, p.Locator,
			p.UserID, p.EditionID, baseRevision,
		}
		if baseGeneration >= 0 {
			query += ` AND reset_generation = ?`
			args = append(args, baseGeneration)
		}
		if timelineGeneration >= 0 {
			query += ` AND EXISTS (SELECT 1 FROM editions WHERE id = progress.edition_id AND timeline_generation = ?)`
			args = append(args, timelineGeneration)
		}
		err = d.QueryRow(query+` RETURNING revision, reset_generation`, args...).Scan(&revision, &generation)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return revision, generation, true, nil
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
	if e.SourceKey != "" {
		id, err := upsertSourceEdition(q, &e.Edition)
		if err != nil {
			return 0, err
		}
		_, err = q.Exec(`UPDATE editions SET page_count=? WHERE id=?`, e.PageCount, id)
		return id, err
	}
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
func (d *DB) FileStatByPath(path string) (int64, int64, int64, bool, error) {
	var size, mtime, mtimeNs int64
	err := d.QueryRow(`SELECT size_bytes, mtime_secs, mtime_ns FROM files WHERE path = ? AND missing = 0`, path).Scan(&size, &mtime, &mtimeNs)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, 0, false, err
	}
	return size, mtime, mtimeNs, true, nil
}

// FileStatByPathWithSHA additionally reports whether the row carries a
// content hash. The games scanner warm-skips only when hasSHA is true: a
// NULL sha256 means the last read failed (or predates migration 0016), and
// skipping would freeze the missing value forever.
func (d *DB) FileStatByPathWithSHA(path string) (int64, int64, int64, bool, bool, error) {
	var size, mtime, mtimeNs int64
	var sha sql.NullString
	err := d.QueryRow(`SELECT size_bytes, mtime_secs, mtime_ns, sha256 FROM files WHERE path = ? AND missing = 0`, path).Scan(&size, &mtime, &mtimeNs, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, false, false, nil
	}
	if err != nil {
		return 0, 0, 0, false, false, err
	}
	return size, mtime, mtimeNs, sha.Valid && sha.String != "", true, nil
}

func (d *DB) SetFileMeta(fileID int64, meta string) error {
	_, err := d.Exec(`UPDATE files SET embedded_meta = ? WHERE id = ?`, meta, fileID)
	return err
}

func (t *Tx) SetFileMeta(fileID int64, meta string) error {
	_, err := t.Exec(`UPDATE files SET embedded_meta = ? WHERE id = ?`, meta, fileID)
	return err
}
