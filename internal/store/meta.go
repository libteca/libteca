package store

import (
	"encoding/json"
)

// GetCached reads a provider_cache row: (response, fetched_at, ok).
func (d *DB) GetCached(provider, key string) (string, int64, bool) {
	var resp string
	var at int64
	err := d.QueryRow(`SELECT response, fetched_at FROM provider_cache WHERE provider = ? AND key = ?`, provider, key).Scan(&resp, &at)
	if err != nil {
		return "", 0, false
	}
	return resp, at, true
}

func (d *DB) PutCached(provider, key, response string) error {
	_, err := d.Exec(`INSERT INTO provider_cache (provider, key, response, fetched_at) VALUES (?,?,?,?)
		ON CONFLICT(provider, key) DO UPDATE SET response = excluded.response, fetched_at = excluded.fetched_at`,
		provider, key, response, nowMilli())
	return err
}

type InboxWork struct {
	ID          int64
	LibraryID   int64
	LibraryName string
	LibraryType string
	Title       string
	Author      *string
	CoverPath   *string
}

// MatchingInbox returns works lacking description AND (author or provider).
// libID > 0 filters to one library. Works with a match-skip marker are
// excluded.
func (d *DB) MatchingInbox(libID int64) ([]InboxWork, error) {
	q := `SELECT w.id, w.library_id, l.name, l.type, w.title, w.author, w.cover_path
		FROM works w JOIN libraries l ON l.id = w.library_id
		WHERE (w.description IS NULL OR w.description = '')
		AND (w.author IS NULL OR w.author = '' OR w.provider IS NULL OR w.provider = '')
		AND NOT EXISTS (SELECT 1 FROM provider_cache pc WHERE pc.provider = 'match-skip' AND pc.key = CAST(w.id AS TEXT))`
	var args []any
	if libID > 0 {
		q += ` AND w.library_id = ?`
		args = append(args, libID)
	}
	q += ` ORDER BY w.id`
	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboxWork
	for rows.Next() {
		var w InboxWork
		if err := rows.Scan(&w.ID, &w.LibraryID, &w.LibraryName, &w.LibraryType, &w.Title, &w.Author, &w.CoverPath); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ApplyWorkMeta records provider identity and (when non-empty) description.
func (d *DB) ApplyWorkMeta(workID int64, description, provider, providerID string) error {
	now := nowMilli()
	if description == "" {
		_, err := d.Exec(`UPDATE works SET provider = ?, provider_id = ?, updated_at = ? WHERE id = ?`,
			provider, providerID, now, workID)
		return err
	}
	_, err := d.Exec(`UPDATE works SET description = ?, provider = ?, provider_id = ?, updated_at = ? WHERE id = ?`,
		description, provider, providerID, now, workID)
	return err
}

// FillEmptyChapters writes chapters to files of single-file m4b editions of
// the work, ONLY where chapters are empty today — ffprobe data is never
// clobbered. Returns the number of files written.
func (d *DB) FillEmptyChapters(workID int64, chapters string) (int64, error) {
	res, err := d.Exec(`UPDATE files SET chapters = ? WHERE id IN (
		SELECT f.id FROM files f
		JOIN editions e ON e.id = f.edition_id
		WHERE e.work_id = ? AND e.format = 'm4b' AND f.missing = 0 AND f.chapters = '[]'
		AND (SELECT COUNT(*) FROM files f2 WHERE f2.edition_id = f.edition_id AND f2.missing = 0) = 1)`,
		chapters, workID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Genres live on works.genres as a JSON array (0008). Settings-KV rows
// from before the migration stay behind — harmless, no longer read.

func (d *DB) SetWorkGenres(workID int64, genres []string) error {
	b, err := json.Marshal(genres)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE works SET genres = ?, updated_at = ? WHERE id = ?`,
		string(b), nowMilli(), workID)
	return err
}

func (d *DB) WorkGenres(workID int64) []string {
	var raw string
	if err := d.QueryRow(`SELECT genres FROM works WHERE id = ?`, workID).Scan(&raw); err != nil {
		return nil
	}
	var genres []string
	if json.Unmarshal([]byte(raw), &genres) != nil {
		return nil
	}
	return genres
}

// EpisodeEdition is the slice of an edition apply-episodes works on.
type EpisodeEdition struct {
	ID          int64
	SeasonNum   int
	EpisodeNum  int
	Title       string
	Description *string
}

// WorkEpisodes returns the TV editions of a work that carry season and
// episode numbers, ordered by (season, episode).
func (d *DB) WorkEpisodes(workID int64) ([]EpisodeEdition, error) {
	rows, err := d.Query(`SELECT id, season_num, episode_num, title, description FROM editions
		WHERE work_id = ? AND season_num IS NOT NULL AND episode_num IS NOT NULL
		ORDER BY season_num, episode_num`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EpisodeEdition
	for rows.Next() {
		var e EpisodeEdition
		if err := rows.Scan(&e.ID, &e.SeasonNum, &e.EpisodeNum, &e.Title, &e.Description); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (d *DB) SetEpisodeTitle(editionID int64, title string) error {
	_, err := d.Exec(`UPDATE editions SET title = ? WHERE id = ?`, title, editionID)
	return err
}

// SetEpisodeDescription fills an empty episode description — existing text
// is never clobbered.
func (d *DB) SetEpisodeDescription(editionID int64, description string) error {
	_, err := d.Exec(`UPDATE editions SET description = ? WHERE id = ? AND (description IS NULL OR description = '')`,
		description, editionID)
	return err
}
