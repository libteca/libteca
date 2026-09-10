package store

import (
	"encoding/json"
	"strconv"
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

// Genres live in the settings KV (works has no genres column; a proper
// migration is the follow-up). Key: genres:work:{id} -> JSON array.

func (d *DB) SetWorkGenres(workID int64, genres []string) error {
	b, err := json.Marshal(genres)
	if err != nil {
		return err
	}
	return d.SetSetting("genres:work/"+strconv.FormatInt(workID, 10), string(b))
}

func (d *DB) WorkGenres(workID int64) []string {
	v, ok := d.GetSetting("genres:work/" + strconv.FormatInt(workID, 10))
	if !ok {
		return nil
	}
	var genres []string
	if json.Unmarshal([]byte(v), &genres) != nil {
		return nil
	}
	return genres
}
