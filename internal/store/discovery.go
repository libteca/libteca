package store

import (
	"database/sql"
	"strings"
)

type ResumeItem struct {
	WorkID       int64
	EditionID    int64
	LibraryID    int64
	LibraryType  string
	Title        string
	Author       *string
	CoverPath    *string
	PositionSecs float64
	DurationSecs float64
	Percent      float64
	UpdatedAt    int64
}

type SearchHit struct {
	WorkID      int64
	LibraryID   int64
	LibraryType string
	Title       string
	Author      *string
	CoverPath   *string
	Percent     *float64
}

type RecentWork struct {
	WorkID      int64
	LibraryID   int64
	LibraryType string
	Title       string
	Author      *string
	CoverPath   *string
	AddedAt     int64
}

func progressPercent(pos, dur float64) float64 {
	if dur <= 0 {
		return 0
	}
	p := pos / dur
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

func coalesceReadPercent(pos, dur float64, pct sql.NullFloat64, page, pages sql.NullInt64) float64 {
	if pct.Valid && pct.Float64 > 0 {
		if pct.Float64 > 1 {
			return 1
		}
		return pct.Float64
	}
	if page.Valid && pages.Valid && pages.Int64 > 0 {
		return progressPercent(float64(page.Int64), float64(pages.Int64))
	}
	return progressPercent(pos, dur)
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ResumeItems returns the authed user's latest in-progress edition per work
// (max progress.updated_at among not-finished rows on that work's editions),
// newest first across all libraries.
func (d *DB) ResumeItems(userID int64, limit int) ([]ResumeItem, error) {
	rows, err := d.Query(`SELECT w.id, e.id, w.library_id, l.type, w.title, w.author, w.cover_path,
		p.edition_position_secs, COALESCE(e.duration_secs, p.duration_secs), p.updated_at,
		p.percent, p.page, e.page_count
		FROM progress p
		JOIN editions e ON e.id = p.edition_id
		JOIN works w ON w.id = e.work_id
		JOIN libraries l ON l.id = w.library_id
		WHERE p.user_id = ? AND p.is_finished = 0
		  AND p.id = (
			SELECT p2.id FROM progress p2
			JOIN editions e2 ON e2.id = p2.edition_id
			WHERE p2.user_id = p.user_id AND p2.is_finished = 0 AND e2.work_id = e.work_id
			ORDER BY p2.updated_at DESC, p2.id DESC LIMIT 1
		  )
		ORDER BY p.updated_at DESC, p.id DESC
		LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResumeItem
	for rows.Next() {
		var it ResumeItem
		var dur, pct sql.NullFloat64
		var page, pages sql.NullInt64
		if err := rows.Scan(&it.WorkID, &it.EditionID, &it.LibraryID, &it.LibraryType, &it.Title,
			&it.Author, &it.CoverPath, &it.PositionSecs, &dur, &it.UpdatedAt, &pct, &page, &pages); err != nil {
			return nil, err
		}
		it.DurationSecs = dur.Float64
		it.Percent = coalesceReadPercent(it.PositionSecs, it.DurationSecs, pct, page, pages)
		out = append(out, it)
	}
	return out, rows.Err()
}

// SearchWorks matches q case-insensitively as a substring of works.title or
// works.author; title matches rank before author-only matches, then title asc.
// Percent is the user's latest progress on the work's latest-progress edition,
// nil when the user has none.
func (d *DB) SearchWorks(userID int64, q string, limit int) ([]SearchHit, error) {
	pat := "%" + likeEscape(strings.ToLower(q)) + "%"
	rows, err := d.Query(`SELECT w.id, w.library_id, l.type, w.title, w.author, w.cover_path,
		(SELECT COALESCE(
			p.percent,
			CASE WHEN e.page_count IS NOT NULL AND e.page_count > 0 AND p.page IS NOT NULL
				THEN p.page * 1.0 / e.page_count END,
			CASE WHEN COALESCE(e.duration_secs, p.duration_secs) > 0
				THEN p.edition_position_secs / COALESCE(e.duration_secs, p.duration_secs) END
		) FROM progress p JOIN editions e ON e.id = p.edition_id
		 WHERE p.user_id = ? AND e.work_id = w.id ORDER BY p.updated_at DESC, p.id DESC LIMIT 1)
		FROM works w JOIN libraries l ON l.id = w.library_id
		WHERE lower(w.title) LIKE ? ESCAPE '\' OR (w.author IS NOT NULL AND lower(w.author) LIKE ? ESCAPE '\')
		ORDER BY (lower(w.title) LIKE ? ESCAPE '\') DESC, lower(w.title) ASC, w.id ASC
		LIMIT ?`, userID, pat, pat, pat, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var pct sql.NullFloat64
		if err := rows.Scan(&h.WorkID, &h.LibraryID, &h.LibraryType, &h.Title,
			&h.Author, &h.CoverPath, &pct); err != nil {
			return nil, err
		}
		if pct.Valid {
			p := pct.Float64
			if p < 0 {
				p = 0
			}
			if p > 1 {
				p = 1
			}
			h.Percent = &p
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// RecentWorks returns works by created_at desc, id desc on ties.
func (d *DB) RecentWorks(limit int) ([]RecentWork, error) {
	rows, err := d.Query(`SELECT w.id, w.library_id, l.type, w.title, w.author, w.cover_path, w.created_at
		FROM works w JOIN libraries l ON l.id = w.library_id
		ORDER BY w.created_at DESC, w.id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecentWork
	for rows.Next() {
		var rw RecentWork
		if err := rows.Scan(&rw.WorkID, &rw.LibraryID, &rw.LibraryType, &rw.Title,
			&rw.Author, &rw.CoverPath, &rw.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, rw)
	}
	return out, rows.Err()
}

func sortDirSQL(dir string) string {
	if dir == "desc" {
		return "DESC"
	}
	return "ASC"
}

// WorksInLibraryFiltered is WorksInLibrary with server-side sort and
// per-user progress filtering. sort: title|author|added|updated; dir:
// asc|desc; filter: all|in_progress|unplayed|finished. sort "title" + dir
// "asc" reproduces WorksInLibrary's ordering exactly.
func (d *DB) WorksInLibraryFiltered(libID, userID int64, sort, dir, filter string) ([]WorkView, error) {
	if dir != "asc" && dir != "desc" {
		if sort == "added" || sort == "updated" {
			dir = "desc"
		} else {
			dir = "asc"
		}
	}
	order := `lower(title) ` + sortDirSQL(dir)
	switch sort {
	case "author":
		order = `lower(coalesce(author,'')) ` + sortDirSQL(dir)
	case "added":
		order = `created_at ` + sortDirSQL(dir) + `, id DESC`
	case "updated":
		order = `updated_at ` + sortDirSQL(dir) + `, id DESC`
	}
	where := `library_id = ?`
	args := []any{libID}
	switch filter {
	case "in_progress":
		where += ` AND EXISTS (SELECT 1 FROM progress p JOIN editions e ON e.id = p.edition_id
			WHERE e.work_id = works.id AND p.user_id = ? AND p.is_finished = 0)`
		args = append(args, userID)
	case "unplayed":
		where += ` AND NOT EXISTS (SELECT 1 FROM progress p JOIN editions e ON e.id = p.edition_id
			WHERE e.work_id = works.id AND p.user_id = ?)`
		args = append(args, userID)
	case "finished":
		where += ` AND EXISTS (SELECT 1 FROM progress p JOIN editions e ON e.id = p.edition_id
			WHERE e.work_id = works.id AND p.user_id = ? AND p.is_finished = 1)`
		args = append(args, userID)
	}

	rows, err := d.Query(`SELECT id, library_id, title, subtitle, author, description, cover_path, created_at, updated_at
		FROM works WHERE `+where+` ORDER BY `+order, args...)
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

	frows, err := d.Query(`SELECT ` + fileCols + ` FROM files WHERE missing = 0 AND edition_id IN
		(SELECT id FROM editions WHERE work_id IN (SELECT id FROM works WHERE library_id = ?))
		ORDER BY edition_id, seq`, libID)
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
	if err := frows.Err(); err != nil {
		return nil, err
	}
	if err := d.attachWorkPercents(userID, libID, out, index); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *DB) attachWorkPercents(userID, libID int64, out []WorkView, index map[int64]int) error {
	if userID == 0 || len(out) == 0 {
		return nil
	}
	rows, err := d.Query(`SELECT e.work_id, p.edition_position_secs, COALESCE(e.duration_secs, p.duration_secs),
		p.percent, p.page, e.page_count
		FROM progress p
		JOIN editions e ON e.id = p.edition_id
		JOIN works w ON w.id = e.work_id
		WHERE p.user_id = ? AND w.library_id = ? AND p.is_finished = 0
		  AND p.id = (
			SELECT p2.id FROM progress p2
			JOIN editions e2 ON e2.id = p2.edition_id
			WHERE p2.user_id = p.user_id AND p2.is_finished = 0 AND e2.work_id = e.work_id
			ORDER BY p2.updated_at DESC, p2.id DESC LIMIT 1
		  )`, userID, libID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var workID int64
		var pos float64
		var dur, pct sql.NullFloat64
		var page, pages sql.NullInt64
		if err := rows.Scan(&workID, &pos, &dur, &pct, &page, &pages); err != nil {
			return err
		}
		wi, ok := index[workID]
		if !ok {
			continue
		}
		p := coalesceReadPercent(pos, dur.Float64, pct, page, pages)
		out[wi].Percent = &p
	}
	return rows.Err()
}
