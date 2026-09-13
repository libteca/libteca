-- +goose NO TRANSACTION
-- Games library type (PLAN-GAMES G1). SQLite cannot ALTER a CHECK
-- constraint, so both tables are rebuilt. Edition formats gain a namespaced
-- 'game-<platform>' form (CHECK keeps the existing list plus the prefix
-- pattern) so new platforms never need another migration; the bare platform
-- tag rides on files.container.
--
-- NO TRANSACTION: the rebuilds drop parent tables while child rows exist,
-- which requires PRAGMA foreign_keys to actually toggle - a no-op inside a
-- transaction. The 0004 rebuild predates this and its Down was never
-- exercised by tests; this one is.

-- +goose Up
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
CREATE TABLE libraries_new (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('movies','tv','music','audiobooks','books','comics','podcasts','games')),
  path TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
INSERT INTO libraries_new SELECT id, name, type, path, created_at FROM libraries;
DROP TABLE libraries;
ALTER TABLE libraries_new RENAME TO libraries;

CREATE TABLE editions_new (
  id INTEGER PRIMARY KEY,
  work_id INTEGER NOT NULL REFERENCES works(id),
  format TEXT NOT NULL CHECK (format IN ('m4b','mp3','epub','pdf','cbz','cbr','video','audio') OR format LIKE 'game-%'),
  title TEXT NOT NULL,
  language TEXT,
  abridged INTEGER NOT NULL DEFAULT 0,
  duration_secs REAL,
  position INTEGER NOT NULL DEFAULT 0,
  season_num INTEGER,
  episode_num INTEGER,
  page_count INTEGER,
  description TEXT,
  created_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO editions_new (id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, page_count, description, created_at)
  SELECT id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, page_count, description, created_at FROM editions;
DROP TABLE editions;
ALTER TABLE editions_new RENAME TO editions;
CREATE INDEX idx_editions_work ON editions(work_id, season_num, episode_num);
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
DELETE FROM progress WHERE edition_id IN (SELECT id FROM editions WHERE format LIKE 'game-%');
DELETE FROM playback_sessions WHERE edition_id IN (SELECT id FROM editions WHERE format LIKE 'game-%');
DELETE FROM files WHERE edition_id IN (SELECT id FROM editions WHERE format LIKE 'game-%');
DELETE FROM editions WHERE format LIKE 'game-%';

CREATE TABLE editions_old (
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
  description TEXT,
  created_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO editions_old (id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, page_count, description, created_at)
  SELECT id, work_id, format, title, language, abridged, duration_secs, position, season_num, episode_num, page_count, description, created_at FROM editions;
DROP TABLE editions;
ALTER TABLE editions_old RENAME TO editions;
CREATE INDEX idx_editions_work ON editions(work_id, season_num, episode_num);

CREATE TABLE libraries_old (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('movies','tv','music','audiobooks','books','comics','podcasts')),
  path TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
INSERT INTO libraries_old SELECT id, name, type, path, created_at FROM libraries WHERE type != 'games';
DROP TABLE libraries;
ALTER TABLE libraries_old RENAME TO libraries;
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
