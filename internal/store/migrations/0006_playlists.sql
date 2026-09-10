-- +goose Up

-- user_id carries ON DELETE CASCADE so store.DeleteUser (which enumerates
-- its dependent tables explicitly) keeps working without modification.

CREATE TABLE playlists (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE playlist_items (
  id INTEGER PRIMARY KEY,
  playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
  edition_id INTEGER NOT NULL REFERENCES editions(id),
  position INTEGER NOT NULL,
  added_at INTEGER NOT NULL,
  UNIQUE (playlist_id, edition_id)
);
CREATE INDEX idx_playlist_items_order ON playlist_items(playlist_id, position);

-- +goose Down

DROP TABLE IF EXISTS playlist_items;
DROP TABLE IF EXISTS playlists;
