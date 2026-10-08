-- +goose Up
CREATE TABLE game_play_sessions (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 edition_id INTEGER NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 reset_generation INTEGER NOT NULL,
 session_id TEXT NOT NULL,
 elapsed_secs REAL NOT NULL CHECK(elapsed_secs >= 0),
 PRIMARY KEY(user_id, edition_id, reset_generation, session_id)
);

-- +goose Down
DROP TABLE game_play_sessions;
