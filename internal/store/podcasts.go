package store

import (
	"database/sql"
	"errors"
	"strings"
)

type Podcast struct {
	ID           int64
	LibraryID    int64
	FeedURL      string
	Title        string
	Author       *string
	Description  *string
	CoverPath    *string
	ETag         *string
	LastModified *string
	LastFetchAt  *int64
	AutoDownload bool
	MaxEpisodes  int
	CreatedAt    int64
}

type PodcastView struct {
	Podcast
	EpisodeCount    int
	DownloadedCount int
}

type PodcastEpisode struct {
	ID             int64
	PodcastID      int64
	GUID           string
	Title          *string
	Description    *string
	PubDate        *int64
	DurationSecs   *float64
	EnclosureURL   string
	EnclosureBytes *int64
	FileID         *int64
	DownloadedAt   *int64
	CreatedAt      int64
}

const podcastCols = `id, library_id, feed_url, title, author, description, cover_path, etag, last_modified, last_fetch_at, auto_download, max_episodes, created_at`
const episodeCols = `id, podcast_id, guid, title, description, pub_date, duration_secs, enclosure_url, enclosure_bytes, file_id, downloaded_at, created_at`

func scanPodcast(row interface{ Scan(...any) error }) (*Podcast, error) {
	var p Podcast
	var autoDL int
	err := row.Scan(&p.ID, &p.LibraryID, &p.FeedURL, &p.Title, &p.Author, &p.Description, &p.CoverPath,
		&p.ETag, &p.LastModified, &p.LastFetchAt, &autoDL, &p.MaxEpisodes, &p.CreatedAt)
	p.AutoDownload = autoDL != 0
	return &p, err
}

func scanEpisode(row interface{ Scan(...any) error }) (*PodcastEpisode, error) {
	var e PodcastEpisode
	err := row.Scan(&e.ID, &e.PodcastID, &e.GUID, &e.Title, &e.Description, &e.PubDate, &e.DurationSecs,
		&e.EnclosureURL, &e.EnclosureBytes, &e.FileID, &e.DownloadedAt, &e.CreatedAt)
	return &e, err
}

func (d *DB) EnsurePodcastsLibrary(path string) (int64, error) {
	var id int64
	err := d.Update(func(tx *Tx) error {
		err := tx.QueryRow(`SELECT id FROM libraries WHERE type = 'podcasts' ORDER BY id LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			res, ierr := tx.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Podcasts','podcasts',?,?)`, path, nowMilli())
			if ierr != nil {
				return ierr
			}
			id, err = res.LastInsertId()
		}
		return err
	})
	return id, err
}

func (d *DB) AddPodcast(p *Podcast) (int64, error) {
	autoDL := 0
	if p.AutoDownload {
		autoDL = 1
	}
	if p.MaxEpisodes <= 0 {
		p.MaxEpisodes = 3
	}
	res, err := d.Exec(`INSERT INTO podcasts (library_id, feed_url, title, author, description, auto_download, max_episodes, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		p.LibraryID, p.FeedURL, p.Title, p.Author, p.Description, autoDL, p.MaxEpisodes, nowMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) Podcast(id int64) (*Podcast, error) {
	p, err := scanPodcast(d.QueryRow(`SELECT `+podcastCols+` FROM podcasts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (d *DB) PodcastByFeedURL(url string) (*Podcast, error) {
	p, err := scanPodcast(d.QueryRow(`SELECT `+podcastCols+` FROM podcasts WHERE feed_url = ?`, url))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (d *DB) Podcasts() ([]Podcast, error) {
	rows, err := d.Query(`SELECT ` + podcastCols + ` FROM podcasts ORDER BY lower(title)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Podcast
	for rows.Next() {
		p, err := scanPodcast(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (d *DB) PodcastsWithCounts() ([]PodcastView, error) {
	rows, err := d.Query(`SELECT p.id, p.library_id, p.feed_url, p.title, p.author, p.description, p.cover_path,
		p.etag, p.last_modified, p.last_fetch_at, p.auto_download, p.max_episodes, p.created_at,
		COUNT(e.id), COUNT(e.file_id)
		FROM podcasts p LEFT JOIN podcast_episodes e ON e.podcast_id = p.id
		GROUP BY p.id ORDER BY lower(p.title)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PodcastView
	for rows.Next() {
		var v PodcastView
		var autoDL int
		if err := rows.Scan(&v.ID, &v.LibraryID, &v.FeedURL, &v.Title, &v.Author, &v.Description, &v.CoverPath,
			&v.ETag, &v.LastModified, &v.LastFetchAt, &autoDL, &v.MaxEpisodes, &v.CreatedAt,
			&v.EpisodeCount, &v.DownloadedCount); err != nil {
			return nil, err
		}
		v.AutoDownload = autoDL != 0
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) UpdatePodcastFetch(id int64, etag, lastModified *string, lastFetchAt int64) error {
	_, err := d.Exec(`UPDATE podcasts SET etag = ?, last_modified = ?, last_fetch_at = ? WHERE id = ?`,
		etag, lastModified, lastFetchAt, id)
	return err
}

func (d *DB) UpdatePodcastMeta(id int64, title string, author, description *string) error {
	_, err := d.Exec(`UPDATE podcasts SET title = ?, author = ?, description = ? WHERE id = ?`,
		title, author, description, id)
	return err
}

func (d *DB) SetPodcastCover(id int64, rel string) error {
	_, err := d.Exec(`UPDATE podcasts SET cover_path = ? WHERE id = ? AND (cover_path IS NULL OR cover_path = '')`,
		rel, id)
	return err
}

func (d *DB) UpdatePodcastSettings(id int64, autoDownload *bool, maxEpisodes *int) error {
	return d.Update(func(tx *Tx) error {
		if autoDownload != nil {
			v := 0
			if *autoDownload {
				v = 1
			}
			if _, err := tx.Exec(`UPDATE podcasts SET auto_download = ? WHERE id = ?`, v, id); err != nil {
				return err
			}
		}
		if maxEpisodes != nil {
			if _, err := tx.Exec(`UPDATE podcasts SET max_episodes = ? WHERE id = ?`, *maxEpisodes, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// PodcastFileRow is a podcast-side view of a files row; unlike FileRec it
// tolerates the NULL edition_id podcast downloads carry.
type PodcastFileRow struct {
	ID      int64
	Path    string
	Missing bool
}

// FilesForPodcast returns all file rows linked to a podcast's episodes,
// including rows already marked missing.
func (d *DB) FilesForPodcast(podcastID int64) ([]PodcastFileRow, error) {
	rows, err := d.Query(`SELECT id, path, missing FROM files WHERE id IN
		(SELECT file_id FROM podcast_episodes WHERE podcast_id = ? AND file_id IS NOT NULL)`, podcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PodcastFileRow
	for rows.Next() {
		var r PodcastFileRow
		var missing int
		if err := rows.Scan(&r.ID, &r.Path, &missing); err != nil {
			return nil, err
		}
		r.Missing = missing != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// FilePath resolves a non-missing file's path for serving.
func (d *DB) FilePath(id int64) (string, error) {
	var path string
	err := d.QueryRow(`SELECT path FROM files WHERE id = ? AND missing = 0`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

// DeleteFilesByIDs removes file rows outright; callers must clear episode
// links first (FK) — used by unsubscribe, which deletes content.
func (d *DB) DeleteFilesByIDs(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_, err := d.Exec(`DELETE FROM files WHERE id IN (`+placeholders+`)`, args...)
	return err
}

func (d *DB) DeletePodcast(id int64) error {
	return d.Update(func(tx *Tx) error {
		if _, err := tx.Exec(`DELETE FROM podcast_episodes WHERE podcast_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM podcasts WHERE id = ?`, id)
		return err
	})
}

func (d *DB) UpsertPodcastEpisode(e *PodcastEpisode) (bool, error) {
	added := false
	err := d.Update(func(tx *Tx) error {
		var id int64
		err := tx.QueryRow(`SELECT id FROM podcast_episodes WHERE podcast_id = ? AND guid = ?`, e.PodcastID, e.GUID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			res, ierr := tx.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, title, description, pub_date, duration_secs, enclosure_url, enclosure_bytes, created_at)
				VALUES (?,?,?,?,?,?,?,?,?)`,
				e.PodcastID, e.GUID, e.Title, e.Description, e.PubDate, e.DurationSecs, e.EnclosureURL, e.EnclosureBytes, nowMilli())
			if ierr != nil {
				return ierr
			}
			e.ID, _ = res.LastInsertId()
			added = true
			return nil
		}
		if err != nil {
			return err
		}
		e.ID = id
		_, err = tx.Exec(`UPDATE podcast_episodes SET title = ?, description = ?, pub_date = ?, duration_secs = ?, enclosure_url = ?, enclosure_bytes = ? WHERE id = ?`,
			e.Title, e.Description, e.PubDate, e.DurationSecs, e.EnclosureURL, e.EnclosureBytes, id)
		return err
	})
	return added, err
}

// PodcastEpisodes lists newest first (NULL pub_date last) for display.
func (d *DB) PodcastEpisodes(podcastID int64) ([]PodcastEpisode, error) {
	rows, err := d.Query(`SELECT `+episodeCols+` FROM podcast_episodes WHERE podcast_id = ? ORDER BY pub_date DESC, id DESC`, podcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PodcastEpisode
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// PendingEpisodes are episodes never handled by the downloader (no file, no
// download attempt recorded), newest first.
func (d *DB) PendingEpisodes(podcastID int64) ([]PodcastEpisode, error) {
	rows, err := d.Query(`SELECT `+episodeCols+` FROM podcast_episodes
		WHERE podcast_id = ? AND file_id IS NULL AND downloaded_at IS NULL ORDER BY pub_date DESC, id DESC`, podcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PodcastEpisode
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// DownloadedEpisodes lists currently-downloaded episodes oldest first
// (NULL pub_date first), the order retention purges in.
func (d *DB) DownloadedEpisodes(podcastID int64) ([]PodcastEpisode, error) {
	rows, err := d.Query(`SELECT `+episodeCols+` FROM podcast_episodes
		WHERE podcast_id = ? AND file_id IS NOT NULL ORDER BY pub_date ASC, id ASC`, podcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PodcastEpisode
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func (d *DB) EpisodeByID(id int64) (*PodcastEpisode, error) {
	e, err := scanEpisode(d.QueryRow(`SELECT `+episodeCols+` FROM podcast_episodes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// EpisodeUsingFilePath resolves which episode (if any) currently links the
// file row owning this path; used to avoid filename collisions.
func (d *DB) EpisodeUsingFilePath(path string) (int64, error) {
	var id int64
	err := d.QueryRow(`SELECT e.id FROM podcast_episodes e JOIN files f ON f.id = e.file_id WHERE f.path = ?`, path).
		Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

// InsertPodcastFile creates (or revives a purged/missing row at the same path)
// a files row with a NULL edition_id — podcast episodes are not editions.
func (d *DB) InsertPodcastFile(path string, sizeBytes, mtimeSecs int64, hash string, durationSecs float64, container string) (int64, error) {
	var id int64
	err := d.Update(func(tx *Tx) error {
		err := tx.QueryRow(`SELECT id FROM files WHERE path = ?`, path).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			res, ierr := tx.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, hash, container, duration_secs, chapters, embedded_meta, missing, probed_at)
				VALUES (NULL,?,1,?,?,?,?,?,'[]','{}',0,?)`,
				path, sizeBytes, mtimeSecs, hash, container, durationSecs, nowMilli())
			if ierr != nil {
				return ierr
			}
			id, err = res.LastInsertId()
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE files SET size_bytes = ?, mtime_secs = ?, hash = ?, container = ?, duration_secs = ?, missing = 0, probed_at = ? WHERE id = ?`,
			sizeBytes, mtimeSecs, hash, container, durationSecs, nowMilli(), id)
		return err
	})
	return id, err
}

func (d *DB) LinkEpisodeFile(episodeID, fileID int64) error {
	_, err := d.Exec(`UPDATE podcast_episodes SET file_id = ?, downloaded_at = ? WHERE id = ?`,
		fileID, nowMilli(), episodeID)
	return err
}

// MarkEpisodeSeen records a handled-but-not-kept episode so the back catalog
// is not re-chewed on every refresh.
func (d *DB) MarkEpisodeSeen(episodeID int64) error {
	_, err := d.Exec(`UPDATE podcast_episodes SET downloaded_at = ? WHERE id = ? AND file_id IS NULL`,
		nowMilli(), episodeID)
	return err
}

func (d *DB) MarkEpisodePurged(episodeID int64) error {
	_, err := d.Exec(`UPDATE podcast_episodes SET file_id = NULL WHERE id = ?`, episodeID)
	return err
}

func (d *DB) MarkFileMissing(fileID int64) error {
	_, err := d.Exec(`UPDATE files SET missing = 1 WHERE id = ?`, fileID)
	return err
}

// EpisodeProgress is per-user playback state for a podcast episode row.
type EpisodeProgress struct {
	UserID       int64
	EpisodeID    int64
	PositionSecs float64
	DurationSecs *float64
	IsFinished   bool
	UpdatedAt    int64
}

const episodeProgressCols = `user_id, episode_id, position_secs, duration_secs, is_finished, updated_at`

func scanEpisodeProgress(row interface{ Scan(...any) error }) (*EpisodeProgress, error) {
	var p EpisodeProgress
	var fin int
	var dur sql.NullFloat64
	err := row.Scan(&p.UserID, &p.EpisodeID, &p.PositionSecs, &dur, &fin, &p.UpdatedAt)
	if dur.Valid {
		v := dur.Float64
		p.DurationSecs = &v
	}
	p.IsFinished = fin != 0
	return &p, err
}

func (d *DB) GetEpisodeProgress(userID, episodeID int64) (*EpisodeProgress, error) {
	p, err := scanEpisodeProgress(d.QueryRow(`SELECT `+episodeProgressCols+` FROM podcast_episode_progress WHERE user_id = ? AND episode_id = ?`, userID, episodeID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (d *DB) SetEpisodeProgress(p *EpisodeProgress) error {
	fin := 0
	if p.IsFinished {
		fin = 1
	}
	_, err := d.Exec(`INSERT INTO podcast_episode_progress (user_id, episode_id, position_secs, duration_secs, is_finished, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(user_id, episode_id) DO UPDATE SET
			position_secs = excluded.position_secs,
			duration_secs = excluded.duration_secs,
			is_finished = excluded.is_finished,
			updated_at = excluded.updated_at`,
		p.UserID, p.EpisodeID, p.PositionSecs, p.DurationSecs, fin, nowMilli())
	return err
}

// EpisodeProgressByPodcast returns the user's progress rows for every episode
// of one podcast, keyed by episode id.
func (d *DB) EpisodeProgressByPodcast(userID, podcastID int64) (map[int64]*EpisodeProgress, error) {
	rows, err := d.Query(`SELECT `+episodeProgressCols+` FROM podcast_episode_progress
		WHERE user_id = ? AND episode_id IN (SELECT id FROM podcast_episodes WHERE podcast_id = ?)`, userID, podcastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*EpisodeProgress{}
	for rows.Next() {
		p, err := scanEpisodeProgress(rows)
		if err != nil {
			return nil, err
		}
		out[p.EpisodeID] = p
	}
	return out, rows.Err()
}

// LatestEpisodeProgressByPodcast returns the user's most recently updated
// progress row per podcast (newest first wins), keyed by podcast id — the
// "continue listening" anchor.
func (d *DB) LatestEpisodeProgressByPodcast(userID int64) (map[int64]*EpisodeProgress, error) {
	rows, err := d.Query(`SELECT p.user_id, p.episode_id, p.position_secs, p.duration_secs, p.is_finished, p.updated_at, e.podcast_id FROM podcast_episode_progress p
		JOIN podcast_episodes e ON e.id = p.episode_id
		WHERE p.user_id = ? ORDER BY p.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*EpisodeProgress{}
	for rows.Next() {
		var podcastID int64
		var p EpisodeProgress
		var fin int
		var dur sql.NullFloat64
		if err := rows.Scan(&p.UserID, &p.EpisodeID, &p.PositionSecs, &dur, &fin, &p.UpdatedAt, &podcastID); err != nil {
			return nil, err
		}
		if dur.Valid {
			v := dur.Float64
			p.DurationSecs = &v
		}
		p.IsFinished = fin != 0
		if _, seen := out[podcastID]; !seen {
			out[podcastID] = &p
		}
	}
	return out, rows.Err()
}
