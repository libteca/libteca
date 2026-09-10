-- +goose NO TRANSACTION

-- Reading (books/comics): page/percent/locator on progress, page_count on
-- editions. The editions rebuild also widens the format CHECK with 'audio':
-- the music scanner inserts format='audio' and 0001's CHECK rejects that
-- insert today. SQLite cannot edit a CHECK in place, so editions is rebuilt
-- with the same single-connection script shape as 0005 (PRAGMA foreign_keys
-- is a no-op inside a transaction). The files table is NOT touched here --
-- 0005 rebuilds it with an explicit column list.

-- +goose Up

ALTER TABLE progress ADD COLUMN page INTEGER;
ALTER TABLE progress ADD COLUMN percent REAL;
ALTER TABLE progress ADD COLUMN locator TEXT;

-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
CREATE TABLE editions_new (
  id INTEGER PRIMARY KEY,
  work_id INTEGER NOT NULL REFERENCES works(id),
  format TEXT NOT NULL CHECK (format IN ('m4b','mp3','epub','pdf','cbz','cbr','video','audio')),
  title TEXT NOT NULL,
  language TEXT,
  abridged INTEGER NOT NULL DEFAULT 0,
  duration_secs REAL,
  position INTEGER NOT NULL DEFAULT 0,
  season_num INTEGER,
  episode_num INTEGER,
  page_count INTEGER,
  created_at INTEGER NOT NULL
);
INSERT INTO editions_new (id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, page_count, created_at)
  SELECT id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, NULL, created_at FROM editions;
DROP TABLE editions;
ALTER TABLE editions_new RENAME TO editions;
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
DELETE FROM progress WHERE edition_id IN (SELECT id FROM editions WHERE format = 'audio');
DELETE FROM playback_sessions WHERE edition_id IN (SELECT id FROM editions WHERE format = 'audio');
DELETE FROM files WHERE edition_id IN (SELECT id FROM editions WHERE format = 'audio');
DELETE FROM editions WHERE format = 'audio';
CREATE TABLE editions_old (
  id INTEGER PRIMARY KEY,
  work_id INTEGER NOT NULL REFERENCES works(id),
  format TEXT NOT NULL CHECK (format IN ('m4b','mp3','epub','pdf','cbz','cbr','video')),
  title TEXT NOT NULL,
  language TEXT,
  abridged INTEGER NOT NULL DEFAULT 0,
  duration_secs REAL,
  position INTEGER NOT NULL DEFAULT 0,
  season_num INTEGER,
  episode_num INTEGER,
  created_at INTEGER NOT NULL
);
INSERT INTO editions_old (id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, created_at)
  SELECT id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, created_at FROM editions;
DROP TABLE editions;
ALTER TABLE editions_old RENAME TO editions;
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
ALTER TABLE progress DROP COLUMN locator;
ALTER TABLE progress DROP COLUMN percent;
ALTER TABLE progress DROP COLUMN page;
