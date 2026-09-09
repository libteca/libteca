-- +goose Up

CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE tokens (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  label TEXT NOT NULL,
  value TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER,
  revoked_at INTEGER
);

CREATE TABLE libraries (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('movies','tv','music','audiobooks','books','comics','podcasts')),
  path TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE works (
  id INTEGER PRIMARY KEY,
  library_id INTEGER NOT NULL REFERENCES libraries(id),
  title TEXT NOT NULL,
  subtitle TEXT,
  author TEXT,
  description TEXT,
  cover_path TEXT,
  provider TEXT,
  provider_id TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_works_match ON works(library_id, lower(title), lower(coalesce(author,'')));

CREATE TABLE editions (
  id INTEGER PRIMARY KEY,
  work_id INTEGER NOT NULL REFERENCES works(id),
  format TEXT NOT NULL CHECK (format IN ('m4b','mp3','epub','pdf','cbz','cbr','video')),
  title TEXT NOT NULL,
  language TEXT,
  abridged INTEGER NOT NULL DEFAULT 0,
  duration_secs REAL,
  position INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE TABLE files (
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
  probed_at INTEGER NOT NULL
);

CREATE TABLE progress (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  edition_id INTEGER NOT NULL REFERENCES editions(id),
  file_id INTEGER REFERENCES files(id),
  file_offset_secs REAL NOT NULL DEFAULT 0,
  edition_position_secs REAL NOT NULL DEFAULT 0,
  duration_secs REAL,
  is_finished INTEGER NOT NULL DEFAULT 0,
  device TEXT,
  updated_at INTEGER NOT NULL,
  UNIQUE (user_id, edition_id)
);

CREATE TABLE playback_sessions (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  edition_id INTEGER NOT NULL REFERENCES editions(id),
  file_id INTEGER,
  started_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  position_secs REAL NOT NULL DEFAULT 0,
  time_listened_secs REAL NOT NULL DEFAULT 0,
  device_info TEXT NOT NULL DEFAULT '{}',
  closed_at INTEGER
);

CREATE TABLE provider_cache (
  provider TEXT NOT NULL,
  key TEXT NOT NULL,
  response TEXT NOT NULL,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (provider, key)
);

CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

-- +goose Down

DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS provider_cache;
DROP TABLE IF EXISTS playback_sessions;
DROP TABLE IF EXISTS progress;
DROP TABLE IF EXISTS files;
DROP TABLE IF EXISTS editions;
DROP TABLE IF EXISTS works;
DROP TABLE IF EXISTS libraries;
DROP TABLE IF EXISTS tokens;
DROP TABLE IF EXISTS users;
