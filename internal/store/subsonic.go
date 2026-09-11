package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Music queries for the Subsonic face. Music model as persisted by the
// scanner: album = work (author = artist), track = edition (position = track
// number), file = the one physical audio file of the edition. Libraries of
// type 'music' are the selector; edition.format is NOT (the scanner writes
// 'audio', which the 0001 CHECK constraint rejects — format is unreliable
// until that is fixed).

const musicAuthorName = `coalesce(nullif(trim(w.author), ''), 'Unknown Artist')`
const musicAuthorKey = `lower(coalesce(nullif(trim(w.author), ''), 'Unknown Artist'))`

const musicAlbumCols = `w.id, w.library_id, w.title, w.subtitle, w.author, w.description, w.cover_path, w.created_at, w.updated_at,
	(SELECT count(*) FROM editions e WHERE e.work_id = w.id),
	(SELECT coalesce(sum(f.duration_secs), 0) FROM files f JOIN editions e2 ON e2.id = f.edition_id WHERE e2.work_id = w.id AND f.missing = 0)`

const musicAlbumFrom = ` FROM works w JOIN libraries l ON l.id = w.library_id WHERE l.type = 'music'`

const musicFileCols = `f.id, f.edition_id, f.path, f.seq, f.size_bytes, f.mtime_secs, f.hash, f.codec, f.video_codec, f.width, f.height, f.container, f.bitrate, f.channels, f.sample_rate, f.duration_secs, f.chapters, f.missing`

const musicSongCols = `w.id, w.library_id, w.title, w.subtitle, w.author, w.description, w.cover_path, w.created_at, w.updated_at,
	e.id, e.work_id, e.format, e.title, e.language, e.abridged, e.duration_secs, e.position, e.season_num, e.episode_num, e.created_at,
	` + musicFileCols

const musicSongFrom = ` FROM editions e
	JOIN works w ON w.id = e.work_id
	JOIN libraries l ON l.id = w.library_id
	JOIN files f ON f.id = (SELECT f2.id FROM files f2 WHERE f2.edition_id = e.id AND f2.missing = 0 ORDER BY f2.seq LIMIT 1)
	WHERE l.type = 'music'`

type MusicArtist struct {
	Key        string
	Name       string
	AlbumCount int
}

type MusicAlbum struct {
	Work
	SongCount int
	Duration  float64
}

type MusicSong struct {
	Work
	Edition
	File FileRec
}

func scanMusicAlbum(rows *sql.Rows, extra ...any) (MusicAlbum, error) {
	var m MusicAlbum
	dest := []any{
		&m.Work.ID, &m.Work.LibraryID, &m.Work.Title, &m.Work.Subtitle, &m.Work.Author,
		&m.Work.Description, &m.Work.CoverPath, &m.Work.CreatedAt, &m.Work.UpdatedAt, &m.SongCount, &m.Duration,
	}
	err := rows.Scan(append(dest, extra...)...)
	return m, err
}

func scanMusicSong(rows *sql.Rows) (MusicSong, error) {
	var s MusicSong
	var abr, missing int
	err := rows.Scan(
		&s.Work.ID, &s.Work.LibraryID, &s.Work.Title, &s.Work.Subtitle, &s.Work.Author, &s.Work.Description, &s.Work.CoverPath, &s.Work.CreatedAt, &s.Work.UpdatedAt,
		&s.Edition.ID, &s.Edition.WorkID, &s.Edition.Format, &s.Edition.Title, &s.Edition.Language, &abr, &s.Edition.DurationSecs, &s.Edition.Position, &s.Edition.SeasonNum, &s.Edition.EpisodeNum, &s.Edition.CreatedAt,
		&s.File.ID, &s.File.EditionID, &s.File.Path, &s.File.Seq, &s.File.SizeBytes, &s.File.MtimeSecs, &s.File.Hash, &s.File.Codec, &s.File.VideoCodec, &s.File.Width, &s.File.Height, &s.File.Container, &s.File.Bitrate, &s.File.Channels, &s.File.SampleRate, &s.File.DurationSecs, &s.File.Chapters, &missing)
	s.Edition.Abridged = abr != 0
	s.File.Missing = missing != 0
	return s, err
}

func (d *DB) MusicArtists() ([]MusicArtist, error) {
	rows, err := d.Query(`SELECT ` + musicAuthorKey + `, max(` + musicAuthorName + `), count(*)
		FROM works w JOIN libraries l ON l.id = w.library_id
		WHERE l.type = 'music'
		GROUP BY ` + musicAuthorKey + `
		ORDER BY ` + musicAuthorKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MusicArtist
	for rows.Next() {
		var a MusicArtist
		if err := rows.Scan(&a.Key, &a.Name, &a.AlbumCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (d *DB) MusicArtistsSearch(q string, limit, offset int) ([]MusicArtist, error) {
	pat := "%" + likeEscape(strings.ToLower(q)) + "%"
	rows, err := d.Query(`SELECT `+musicAuthorKey+`, max(`+musicAuthorName+`), count(*)
		FROM works w JOIN libraries l ON l.id = w.library_id
		WHERE l.type = 'music' AND coalesce(nullif(w.author_l, ''), `+musicAuthorKey+`) LIKE ? ESCAPE '\'
		GROUP BY `+musicAuthorKey+`
		ORDER BY `+musicAuthorKey+`
		LIMIT ? OFFSET ?`, pat, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MusicArtist
	for rows.Next() {
		var a MusicArtist
		if err := rows.Scan(&a.Key, &a.Name, &a.AlbumCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (d *DB) musicAlbums(suffix string, args ...any) ([]MusicAlbum, error) {
	rows, err := d.Query(`SELECT `+musicAlbumCols+musicAlbumFrom+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MusicAlbum
	for rows.Next() {
		m, err := scanMusicAlbum(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) MusicAlbumsNewest(limit, offset int) ([]MusicAlbum, error) {
	return d.musicAlbums(` ORDER BY w.created_at DESC, w.id DESC LIMIT ? OFFSET ?`, limit, offset)
}

func (d *DB) MusicAlbumsRandom(limit, offset int) ([]MusicAlbum, error) {
	return d.musicAlbums(` ORDER BY random() LIMIT ? OFFSET ?`, limit, offset)
}

func (d *DB) MusicAlbumsByName(limit, offset int) ([]MusicAlbum, error) {
	return d.musicAlbums(` ORDER BY lower(w.title), w.id LIMIT ? OFFSET ?`, limit, offset)
}

func (d *DB) MusicAlbumsByArtistOrder(limit, offset int) ([]MusicAlbum, error) {
	return d.musicAlbums(` ORDER BY `+musicAuthorKey+`, lower(w.title), w.id LIMIT ? OFFSET ?`, limit, offset)
}

func (d *DB) MusicAlbumsByArtistKey(key string) ([]MusicAlbum, error) {
	return d.musicAlbums(` AND `+musicAuthorKey+` = ? ORDER BY lower(w.title), w.id`, key)
}

func (d *DB) MusicAlbumByID(workID int64) (*MusicAlbum, error) {
	albums, err := d.musicAlbums(` AND w.id = ?`, workID)
	if err != nil {
		return nil, err
	}
	if len(albums) == 0 {
		return nil, ErrNotFound
	}
	return &albums[0], nil
}

func (d *DB) MusicAlbumsSearch(q string, limit, offset int) ([]MusicAlbum, error) {
	pat := "%" + likeEscape(strings.ToLower(q)) + "%"
	return d.musicAlbums(` AND (coalesce(w.title_l, lower(w.title)) LIKE ? ESCAPE '\' OR coalesce(nullif(w.author_l, ''), `+musicAuthorKey+`) LIKE ? ESCAPE '\')
		ORDER BY lower(w.title), w.id LIMIT ? OFFSET ?`, pat, pat, limit, offset)
}

func (d *DB) MusicAlbumsRecent(userID int64, limit, offset int) ([]MusicAlbum, error) {
	return d.musicAlbumsProgress(` ORDER BY max(p.updated_at) DESC, w.id DESC LIMIT ? OFFSET ?`, userID, limit, offset)
}

func (d *DB) MusicAlbumsFrequent(userID int64, limit, offset int) ([]MusicAlbum, error) {
	return d.musicAlbumsProgress(` ORDER BY count(*) DESC, max(p.updated_at) DESC, w.id DESC LIMIT ? OFFSET ?`, userID, limit, offset)
}

func (d *DB) musicAlbumsProgress(order string, userID int64, limit, offset int) ([]MusicAlbum, error) {
	rows, err := d.Query(`SELECT `+musicAlbumCols+`, max(p.updated_at)
		FROM works w
		JOIN libraries l ON l.id = w.library_id
		JOIN editions e ON e.work_id = w.id
		JOIN progress p ON p.edition_id = e.id AND p.user_id = ?
		WHERE l.type = 'music'
		GROUP BY w.id`+order, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MusicAlbum
	for rows.Next() {
		var lastPlayed int64
		m, err := scanMusicAlbum(rows, &lastPlayed)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) MusicSongsForWork(workID int64) ([]MusicSong, error) {
	rows, err := d.Query(`SELECT `+musicSongCols+musicSongFrom+` AND e.work_id = ? ORDER BY e.position, e.id`, workID)
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

func (d *DB) MusicSongByID(editionID int64) (*MusicSong, error) {
	rows, err := d.Query(`SELECT `+musicSongCols+musicSongFrom+` AND e.id = ? LIMIT 1`, editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	s, err := scanMusicSong(rows)
	if err != nil {
		return nil, err
	}
	if errors.Is(rows.Err(), sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &s, rows.Err()
}

func (d *DB) MusicSongsSearch(q string, limit, offset int) ([]MusicSong, error) {
	pat := "%" + likeEscape(strings.ToLower(q)) + "%"
	rows, err := d.Query(`SELECT `+musicSongCols+musicSongFrom+` AND lower(e.title) LIKE ? ESCAPE '\'
		ORDER BY lower(w.title), e.position, e.id LIMIT ? OFFSET ?`, pat, limit, offset)
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

func (d *DB) GetSetting(key string) (string, bool) {
	var v string
	err := d.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.Exec(`INSERT INTO settings (key, value) VALUES (?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (d *DB) DeleteSetting(key string) error {
	_, err := d.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}
