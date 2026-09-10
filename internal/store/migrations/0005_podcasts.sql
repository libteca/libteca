-- +goose NO TRANSACTION

-- Podcast downloads park in the files table with edition_id NULL (episodes are
-- not works/editions). SQLite cannot drop a NOT NULL, so files is rebuilt.
-- The rebuild runs as ONE multi-statement script on ONE connection (modernc
-- executes scripts single-conn), because PRAGMA foreign_keys is a no-op inside
-- a transaction and must share the connection with the DDL.

-- +goose Up

CREATE TABLE podcasts (
  id INTEGER PRIMARY KEY,
  library_id INTEGER NOT NULL REFERENCES libraries(id),
  feed_url TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  author TEXT,
  description TEXT,
  cover_path TEXT,
  etag TEXT,
  last_modified TEXT,
  last_fetch_at INTEGER,
  auto_download INTEGER NOT NULL DEFAULT 1,
  max_episodes INTEGER NOT NULL DEFAULT 3,
  created_at INTEGER NOT NULL
);

CREATE TABLE podcast_episodes (
  id INTEGER PRIMARY KEY,
  podcast_id INTEGER NOT NULL REFERENCES podcasts(id),
  guid TEXT NOT NULL,
  title TEXT,
  description TEXT,
  pub_date INTEGER,
  duration_secs REAL,
  enclosure_url TEXT NOT NULL,
  enclosure_bytes INTEGER,
  file_id INTEGER REFERENCES files(id),
  downloaded_at INTEGER,
  created_at INTEGER NOT NULL,
  UNIQUE (podcast_id, guid)
);
CREATE INDEX idx_podcast_episodes_podcast ON podcast_episodes(podcast_id, pub_date);

-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
CREATE TABLE files_new (
  id INTEGER PRIMARY KEY,
  edition_id INTEGER REFERENCES editions(id),
  path TEXT NOT NULL UNIQUE,
  seq INTEGER NOT NULL,
  size_bytes INTEGER NOT NULL,
  mtime_secs INTEGER NOT NULL,
  hash TEXT,
  codec TEXT, container TEXT, bitrate INTEGER, channels INTEGER, sample_rate INTEGER,
  duration_secs REAL NOT NULL,
  chapters TEXT NOT NULL DEFAULT '[]',
  embedded_meta TEXT NOT NULL DEFAULT '{}',
  missing INTEGER NOT NULL DEFAULT 0,
  probed_at INTEGER NOT NULL,
  video_codec TEXT, width INTEGER, height INTEGER
);
INSERT INTO files_new (id, edition_id, path, seq, size_bytes, mtime_secs, hash, codec, container, bitrate, channels, sample_rate, duration_secs, chapters, embedded_meta, missing, probed_at, video_codec, width, height)
  SELECT id, edition_id, path, seq, size_bytes, mtime_secs, hash, codec, container, bitrate, channels, sample_rate, duration_secs, chapters, embedded_meta, missing, probed_at, video_codec, width, height FROM files;
DROP TABLE files;
ALTER TABLE files_new RENAME TO files;
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
DROP TABLE IF EXISTS podcast_episodes;
DROP TABLE IF EXISTS podcasts;
DELETE FROM files WHERE edition_id IS NULL;
CREATE TABLE files_old (
  id INTEGER PRIMARY KEY,
  edition_id INTEGER NOT NULL REFERENCES editions(id),
  path TEXT NOT NULL UNIQUE,
  seq INTEGER NOT NULL,
  size_bytes INTEGER NOT NULL,
  mtime_secs INTEGER NOT NULL,
  hash TEXT,
  codec TEXT, container TEXT, bitrate INTEGER, channels INTEGER, sample_rate INTEGER,
  duration_secs REAL NOT NULL,
  chapters TEXT NOT NULL DEFAULT '[]',
  embedded_meta TEXT NOT NULL DEFAULT '{}',
  missing INTEGER NOT NULL DEFAULT 0,
  probed_at INTEGER NOT NULL,
  video_codec TEXT, width INTEGER, height INTEGER
);
INSERT INTO files_old (id, edition_id, path, seq, size_bytes, mtime_secs, hash, codec, container, bitrate, channels, sample_rate, duration_secs, chapters, embedded_meta, missing, probed_at, video_codec, width, height)
  SELECT id, edition_id, path, seq, size_bytes, mtime_secs, hash, codec, container, bitrate, channels, sample_rate, duration_secs, chapters, embedded_meta, missing, probed_at, video_codec, width, height FROM files;
DROP TABLE files;
ALTER TABLE files_old RENAME TO files;
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
