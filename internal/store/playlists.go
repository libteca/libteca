package store

import (
	"database/sql"
	"errors"
)

// Playlists are ordered lists of editions, format-agnostic (music tracks,
// audiobook chapters, any edition row). Scoping is v1 owner-only: Subsonic
// public/shared playlists are not modeled (no public flag, no sharing), so
// ListPlaylists(userID) with userID 0 = all users mirrors Tokens.

type Playlist struct {
	ID           int64
	UserID       int64
	Name         string
	Owner        string
	SongCount    int
	DurationSecs float64
	CreatedAt    int64
	UpdatedAt    int64
}

type PlaylistItem struct {
	EditionID    int64
	Position     int64
	AddedAt      int64
	Title        string
	Format       string
	DurationSecs float64
	WorkTitle    string
	WorkAuthor   *string
	CoverPath    *string
}

const playlistItemDur = `coalesce(e.duration_secs, (SELECT sum(f.duration_secs) FROM files f WHERE f.edition_id = e.id AND f.missing = 0), 0)`

const playlistCols = `p.id, p.user_id, p.name, u.name, p.created_at, p.updated_at,
	(SELECT count(*) FROM playlist_items pi WHERE pi.playlist_id = p.id),
	(SELECT coalesce(sum(` + playlistItemDur + `), 0) FROM playlist_items pi JOIN editions e ON e.id = pi.edition_id WHERE pi.playlist_id = p.id)`

func (d *DB) CreatePlaylist(userID int64, name string) (int64, error) {
	now := nowMilli()
	res, err := d.Exec(`INSERT INTO playlists (user_id, name, created_at, updated_at) VALUES (?,?,?,?)`,
		userID, name, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) Playlist(id int64) (*Playlist, error) {
	var p Playlist
	err := d.QueryRow(`SELECT `+playlistCols+` FROM playlists p JOIN users u ON u.id = p.user_id WHERE p.id = ?`, id).
		Scan(&p.ID, &p.UserID, &p.Name, &p.Owner, &p.CreatedAt, &p.UpdatedAt, &p.SongCount, &p.DurationSecs)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &p, err
}

// ListPlaylists lists playlists; userID 0 means all users.
func (d *DB) ListPlaylists(userID int64) ([]Playlist, error) {
	rows, err := d.Query(`SELECT `+playlistCols+` FROM playlists p JOIN users u ON u.id = p.user_id
		WHERE (? = 0 OR p.user_id = ?) ORDER BY p.id`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Playlist
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.Owner, &p.CreatedAt, &p.UpdatedAt, &p.SongCount, &p.DurationSecs); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlaylistDetail returns the playlist row plus its items joined with edition
// and work data, ordered by position.
func (d *DB) PlaylistDetail(id int64) (*Playlist, []PlaylistItem, error) {
	p, err := d.Playlist(id)
	if err != nil {
		return nil, nil, err
	}
	items, err := d.PlaylistItems(id)
	if err != nil {
		return nil, nil, err
	}
	return p, items, nil
}

func (d *DB) PlaylistItems(id int64) ([]PlaylistItem, error) {
	rows, err := d.Query(`SELECT pi.edition_id, pi.position, pi.added_at, e.title, e.format, `+playlistItemDur+`, w.title, w.author, w.cover_path
		FROM playlist_items pi
		JOIN editions e ON e.id = pi.edition_id
		JOIN works w ON w.id = e.work_id
		WHERE pi.playlist_id = ?
		ORDER BY pi.position, pi.edition_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlaylistItem
	for rows.Next() {
		var it PlaylistItem
		if err := rows.Scan(&it.EditionID, &it.Position, &it.AddedAt, &it.Title, &it.Format, &it.DurationSecs, &it.WorkTitle, &it.WorkAuthor, &it.CoverPath); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (d *DB) RenamePlaylist(id int64, name string) error {
	res, err := d.Exec(`UPDATE playlists SET name = ?, updated_at = ? WHERE id = ?`, name, nowMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (d *DB) DeletePlaylist(id int64) error {
	res, err := d.Exec(`DELETE FROM playlists WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddPlaylistItem appends the edition at max(position)+1. Adding an edition
// already present is a no-op (UNIQUE(playlist_id, edition_id)).
func (d *DB) AddPlaylistItem(playlistID, editionID int64) error {
	var one int
	if err := d.QueryRow(`SELECT 1 FROM editions WHERE id = ?`, editionID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	res, err := d.Exec(`INSERT INTO playlist_items (playlist_id, edition_id, position, added_at)
		VALUES (?,?,coalesce((SELECT max(position) + 1 FROM playlist_items WHERE playlist_id = ?), 1), ?)
		ON CONFLICT(playlist_id, edition_id) DO NOTHING`,
		playlistID, editionID, playlistID, nowMilli())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		_, err = d.Exec(`UPDATE playlists SET updated_at = ? WHERE id = ?`, nowMilli(), playlistID)
	}
	return err
}

func (d *DB) RemovePlaylistItem(playlistID, editionID int64) error {
	res, err := d.Exec(`DELETE FROM playlist_items WHERE playlist_id = ? AND edition_id = ?`, playlistID, editionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := d.compactPlaylistItems(playlistID); err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE playlists SET updated_at = ? WHERE id = ?`, nowMilli(), playlistID)
	return err
}

// ClearPlaylistItems removes every item, keeping the playlist row.
func (d *DB) ClearPlaylistItems(playlistID int64) error {
	if _, err := d.Exec(`DELETE FROM playlist_items WHERE playlist_id = ?`, playlistID); err != nil {
		return err
	}
	_, err := d.Exec(`UPDATE playlists SET updated_at = ? WHERE id = ?`, nowMilli(), playlistID)
	return err
}

// ReorderPlaylistItem moves an item to newPosition (1-based, clamped to the
// item count); positions are compacted back to a contiguous 1..n sequence.
func (d *DB) ReorderPlaylistItem(playlistID, editionID int64, newPosition int) error {
	var old sql.NullInt64
	var count int64
	if err := d.QueryRow(`SELECT
			(SELECT position FROM playlist_items WHERE playlist_id = ? AND edition_id = ?),
			(SELECT count(*) FROM playlist_items WHERE playlist_id = ?)`,
		playlistID, editionID, playlistID).Scan(&old, &count); err != nil {
		return err
	}
	if !old.Valid || count == 0 {
		return ErrNotFound
	}
	nw := int64(newPosition)
	if nw < 1 {
		nw = 1
	}
	if nw > count {
		nw = count
	}
	if nw == old.Int64 {
		return nil
	}
	if _, err := d.Exec(`UPDATE playlist_items SET position = CASE
			WHEN edition_id = ? THEN ?
			WHEN ? > ? AND position > ? AND position <= ? THEN position - 1
			WHEN ? < ? AND position >= ? AND position < ? THEN position + 1
			ELSE position
		END WHERE playlist_id = ?`,
		editionID, nw,
		nw, old.Int64, old.Int64, nw,
		nw, old.Int64, nw, old.Int64,
		playlistID); err != nil {
		return err
	}
	if err := d.compactPlaylistItems(playlistID); err != nil {
		return err
	}
	_, err := d.Exec(`UPDATE playlists SET updated_at = ? WHERE id = ?`, nowMilli(), playlistID)
	return err
}

func (d *DB) compactPlaylistItems(playlistID int64) error {
	_, err := d.Exec(`UPDATE playlist_items SET position = (
			SELECT rn FROM (
				SELECT id, ROW_NUMBER() OVER (ORDER BY position, id) AS rn
				FROM playlist_items WHERE playlist_id = ?
			) r WHERE r.id = playlist_items.id
		) WHERE playlist_id = ?`, playlistID, playlistID)
	return err
}

// PlaylistMusicSongs returns the playlist's music-library editions as
// MusicSongs in playlist order, for the Subsonic face (non-music items have
// no Subsonic representation and are skipped here, though they still count
// in the playlist's stats).
func (d *DB) PlaylistMusicSongs(playlistID int64) ([]MusicSong, error) {
	rows, err := d.Query(`SELECT `+musicSongCols+musicSongFrom+` AND e.id IN (SELECT edition_id FROM playlist_items WHERE playlist_id = ?)
		ORDER BY (SELECT position FROM playlist_items pi WHERE pi.playlist_id = ? AND pi.edition_id = e.id), e.id`,
		playlistID, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MusicSong
	for rows.Next() {
		s, err := scanMusicSong(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
