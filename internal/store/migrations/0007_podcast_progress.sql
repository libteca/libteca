-- +goose Up

-- Podcast episodes are not works/editions, so the edition progress table
-- cannot hold them; per-user episode progress gets its own table keyed on
-- (user, episode). Both FKs cascade: deleting a user or a podcast (which
-- deletes its episodes) takes the progress rows with it.

CREATE TABLE podcast_episode_progress (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  episode_id INTEGER NOT NULL REFERENCES podcast_episodes(id) ON DELETE CASCADE,
  position_secs REAL NOT NULL DEFAULT 0,
  duration_secs REAL,
  is_finished INTEGER DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, episode_id)
);

-- +goose Down

DROP TABLE IF EXISTS podcast_episode_progress;
