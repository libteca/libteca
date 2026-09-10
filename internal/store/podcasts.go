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
	err := d.QueryRow(`SELECT id FROM libraries WHERE type = 'podcasts' ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, ierr := d.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Podcasts','podcasts',?,?)`, path, nowMilli())
		if ierr != nil {
			return 0, ierr
		}
		return res.LastInsertId()
	}
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
	if autoDownload != nil {
		v := 0
		if *autoDownload {
			v = 1
		}
		if _, err := d.Exec(`UPDATE podcasts SET auto_download = ? WHERE id = ?`, v, id); err != nil {
			return err
		}
	}
	if maxEpisodes != nil {
		if _, err := d.Exec(`UPDATE podcasts SET max_episodes = ? WHERE id = ?`, *maxEpisodes, id); err != nil {
			return err
		}
	}
	return nil
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
	if _, err := d.Exec(`DELETE FROM podcast_episodes WHERE podcast_id = ?`, id); err != nil {
		return err
	}
	_, err := d.Exec(`DELETE FROM podcasts WHERE id = ?`, id)
	return err
}

func (d *DB) UpsertPodcastEpisode(e *PodcastEpisode) (bool, error) {
	var id int64
	err := d.QueryRow(`SELECT id FROM podcast_episodes WHERE podcast_id = ? AND guid = ?`, e.PodcastID, e.GUID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, ierr := d.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, title, description, pub_date, duration_secs, enclosure_url, enclosure_bytes, created_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			e.PodcastID, e.GUID, e.Title, e.Description, e.PubDate, e.DurationSecs, e.EnclosureURL, e.EnclosureBytes, nowMilli())
		if ierr != nil {
			return false, ierr
		}
		e.ID, _ = res.LastInsertId()
		return true, nil
	}
	if err != nil {
		return false, err
	}
	e.ID = id
	_, err = d.Exec(`UPDATE podcast_episodes SET title = ?, description = ?, pub_date = ?, duration_secs = ?, enclosure_url = ?, enclosure_bytes = ? WHERE id = ?`,
		e.Title, e.Description, e.PubDate, e.DurationSecs, e.EnclosureURL, e.EnclosureBytes, id)
	return false, err
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
	err := d.QueryRow(`SELECT id FROM files WHERE path = ?`, path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, ierr := d.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, hash, container, duration_secs, chapters, embedded_meta, missing, probed_at)
			VALUES (NULL,?,1,?,?,?,?,?,'[]','{}',0,?)`,
			path, sizeBytes, mtimeSecs, hash, container, durationSecs, nowMilli())
		if ierr != nil {
			return 0, ierr
		}
		return res.LastInsertId()
	}
	if err != nil {
		return 0, err
	}
	_, err = d.Exec(`UPDATE files SET size_bytes = ?, mtime_secs = ?, hash = ?, container = ?, duration_secs = ?, missing = 0, probed_at = ? WHERE id = ?`,
		sizeBytes, mtimeSecs, hash, container, durationSecs, nowMilli(), id)
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
