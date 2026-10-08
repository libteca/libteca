package store

import (
	"context"
	"database/sql"
	"errors"
)

func (d *DB) LibraryCtx(ctx context.Context, id int64) (*Library, error) {
	var l Library
	err := d.QueryRowContext(ctx, `SELECT id, name, type, path, created_at FROM libraries WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Type, &l.Path, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &l, err
}

func (d *DB) EditionByIDCtx(ctx context.Context, id int64) (*EditionView, error) {
	var e Edition
	var abr int
	err := d.QueryRowContext(ctx, `SELECT id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, created_at FROM editions WHERE id = ?`, id).
		Scan(&e.ID, &e.WorkID, &e.Format, &e.Title, &e.Language, &abr, &e.DurationSecs, &e.Position, &e.SeasonNum, &e.EpisodeNum, &e.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.Abridged = abr != 0

	rows, err := d.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE edition_id = ? AND missing = 0 ORDER BY seq`, id)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ev.Files) == 0 {
		return nil, ErrNotFound
	}
	return ev, nil
}

func (d *DB) WorkViewByIDCtx(ctx context.Context, id int64) (*WorkView, error) {
	var w Work
	err := d.QueryRowContext(ctx, `SELECT id, library_id, title, subtitle, author, description, cover_path, created_at, updated_at FROM works WHERE id = ?`, id).
		Scan(&w.ID, &w.LibraryID, &w.Title, &w.Subtitle, &w.Author, &w.Description, &w.CoverPath, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	out := &WorkView{Work: w}
	erows, err := d.QueryContext(ctx, `SELECT id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, created_at FROM editions WHERE work_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var e Edition
		var abr int
		if err := erows.Scan(&e.ID, &e.WorkID, &e.Format, &e.Title, &e.Language, &abr, &e.DurationSecs, &e.Position, &e.SeasonNum, &e.EpisodeNum, &e.CreatedAt); err != nil {
			erows.Close()
			return nil, err
		}
		e.Abridged = abr != 0
		out.Editions = append(out.Editions, EditionView{Edition: e})
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return nil, err
	}
	if len(out.Editions) == 0 {
		return out, nil
	}
	frows, err := d.QueryContext(ctx, `SELECT `+fileCols+` FROM files WHERE missing = 0 AND edition_id IN (SELECT id FROM editions WHERE work_id = ?) ORDER BY edition_id, seq`, id)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	pos := map[int64]int{}
	for i := range out.Editions {
		pos[out.Editions[i].ID] = i
	}
	for frows.Next() {
		f, err := scanFile(frows)
		if err != nil {
			return nil, err
		}
		i, ok := pos[f.EditionID]
		if !ok {
			continue
		}
		ev := &out.Editions[i]
		cum := f.DurationSecs
		if n := len(ev.CumDurations); n > 0 {
			cum += ev.CumDurations[n-1]
		}
		ev.CumDurations = append(ev.CumDurations, cum)
		ev.Files = append(ev.Files, f)
	}
	return out, frows.Err()
}
