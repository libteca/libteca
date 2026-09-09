package store

import (
	"database/sql"
	"errors"
)

type WorkView struct {
	Work
	Editions []EditionView
}

type EditionView struct {
	Edition
	Files        []FileRec
	CumDurations []float64
}

func (e *EditionView) TotalDuration() float64 {
	if len(e.CumDurations) == 0 {
		return 0
	}
	return e.CumDurations[len(e.CumDurations)-1]
}

func (e *EditionView) Locate(position float64) (fileID int64, offset float64) {
	for i, cum := range e.CumDurations {
		if position < cum || i == len(e.CumDurations)-1 {
			prev := 0.0
			if i > 0 {
				prev = e.CumDurations[i-1]
			}
			return e.Files[i].ID, position - prev
		}
	}
	if len(e.Files) > 0 {
		return e.Files[0].ID, 0
	}
	return 0, 0
}

const fileCols = `id, edition_id, path, seq, size_bytes, mtime_secs, hash, codec, video_codec, width, height, container, bitrate, channels, sample_rate, duration_secs, chapters, missing`

func scanFile(rows *sql.Rows) (FileRec, error) {
	var f FileRec
	var missing int
	err := rows.Scan(&f.ID, &f.EditionID, &f.Path, &f.Seq, &f.SizeBytes, &f.MtimeSecs, &f.Hash, &f.Codec, &f.VideoCodec, &f.Width, &f.Height, &f.Container, &f.Bitrate, &f.Channels, &f.SampleRate, &f.DurationSecs, &f.Chapters, &missing)
	f.Missing = missing != 0
	return f, err
}

func (d *DB) WorksInLibrary(libID int64) ([]WorkView, error) {
	rows, err := d.Query(`SELECT id, library_id, title, subtitle, author, description, cover_path, created_at, updated_at FROM works WHERE library_id = ? ORDER BY lower(title)`, libID)
	if err != nil {
		return nil, err
	}
	var out []WorkView
	index := map[int64]int{}
	for rows.Next() {
		var w Work
		if err := rows.Scan(&w.ID, &w.LibraryID, &w.Title, &w.Subtitle, &w.Author, &w.Description, &w.CoverPath, &w.CreatedAt, &w.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		index[w.ID] = len(out)
		out = append(out, WorkView{Work: w})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	erows, err := d.Query(`SELECT id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, created_at FROM editions WHERE work_id IN (SELECT id FROM works WHERE library_id = ?) ORDER BY id`, libID)
	if err != nil {
		return nil, err
	}
	edIndex := map[int64][2]int{}
	for erows.Next() {
		var e Edition
		var abr int
		if err := erows.Scan(&e.ID, &e.WorkID, &e.Format, &e.Title, &e.Language, &abr, &e.DurationSecs, &e.Position, &e.SeasonNum, &e.EpisodeNum, &e.CreatedAt); err != nil {
			erows.Close()
			return nil, err
		}
		e.Abridged = abr != 0
		wi, ok := index[e.WorkID]
		if !ok {
			continue
		}
		edIndex[e.ID] = [2]int{wi, len(out[wi].Editions)}
		out[wi].Editions = append(out[wi].Editions, EditionView{Edition: e})
	}
	erows.Close()

	frows, err := d.Query(`SELECT ` + fileCols + ` FROM files WHERE missing = 0 ORDER BY edition_id, seq`)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		f, err := scanFile(frows)
		if err != nil {
			return nil, err
		}
		pos, ok := edIndex[f.EditionID]
		if !ok {
			continue
		}
		ev := &out[pos[0]].Editions[pos[1]]
		cum := f.DurationSecs
		if n := len(ev.CumDurations); n > 0 {
			cum += ev.CumDurations[n-1]
		}
		ev.CumDurations = append(ev.CumDurations, cum)
		ev.Files = append(ev.Files, f)
	}
	return out, frows.Err()
}

func (d *DB) EditionByID(id int64) (*EditionView, error) {
	var e Edition
	var abr int
	err := d.QueryRow(`SELECT id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, created_at FROM editions WHERE id = ?`, id).
		Scan(&e.ID, &e.WorkID, &e.Format, &e.Title, &e.Language, &abr, &e.DurationSecs, &e.Position, &e.SeasonNum, &e.EpisodeNum, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.Abridged = abr != 0

	rows, err := d.Query(`SELECT `+fileCols+` FROM files WHERE edition_id = ? AND missing = 0 ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ev := &EditionView{Edition: e}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		cum := f.DurationSecs
		if n := len(ev.CumDurations); n > 0 {
			cum += ev.CumDurations[n-1]
		}
		ev.CumDurations = append(ev.CumDurations, cum)
		ev.Files = append(ev.Files, f)
	}
	if len(ev.Files) == 0 {
		return nil, ErrNotFound
	}
	return ev, rows.Err()
}

func (d *DB) WorkByID(id int64) (*Work, error) {
	var w Work
	err := d.QueryRow(`SELECT id, library_id, title, subtitle, author, description, cover_path, created_at, updated_at FROM works WHERE id = ?`, id).
		Scan(&w.ID, &w.LibraryID, &w.Title, &w.Subtitle, &w.Author, &w.Description, &w.CoverPath, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &w, err
}

func (d *DB) FileByID(id int64) (*FileRec, error) {
	rows, err := d.Query(`SELECT `+fileCols+` FROM files WHERE id = ? AND missing = 0`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	f, err := scanFile(rows)
	return &f, err
}
