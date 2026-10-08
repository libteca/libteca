-- +goose Up
ALTER TABLE podcast_episode_progress ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE podcast_episode_progress ADD COLUMN reset_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE podcast_episode_progress ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE podcast_episodes ADD COLUMN media_generation INTEGER NOT NULL DEFAULT 1;
CREATE TABLE media_operation_receipts (
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 operation_id TEXT NOT NULL,
 envelope TEXT NOT NULL,
 result TEXT NOT NULL,
 PRIMARY KEY(user_id, operation_id)
);
-- +goose StatementBegin
CREATE TRIGGER podcast_media_generation AFTER UPDATE OF file_id,duration_secs,enclosure_url ON podcast_episodes
WHEN old.file_id IS NOT new.file_id OR old.duration_secs IS NOT new.duration_secs OR old.enclosure_url IS NOT new.enclosure_url
BEGIN UPDATE podcast_episodes SET media_generation=old.media_generation+1 WHERE id=new.id; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER podcast_file_generation AFTER UPDATE OF path,hash,sha256,size_bytes,mtime_secs,mtime_ns,missing,duration_secs ON files
WHEN old.path IS NOT new.path OR old.hash IS NOT new.hash OR old.sha256 IS NOT new.sha256 OR old.size_bytes IS NOT new.size_bytes OR old.mtime_secs IS NOT new.mtime_secs OR old.mtime_ns IS NOT new.mtime_ns OR old.missing IS NOT new.missing OR old.duration_secs IS NOT new.duration_secs
BEGIN UPDATE podcast_episodes SET media_generation=media_generation+1 WHERE file_id=new.id; END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER podcast_file_generation;
DROP TRIGGER podcast_media_generation;
DROP TABLE media_operation_receipts;
ALTER TABLE podcast_episodes DROP COLUMN media_generation;
ALTER TABLE podcast_episode_progress DROP COLUMN deleted;
ALTER TABLE podcast_episode_progress DROP COLUMN reset_generation;
ALTER TABLE podcast_episode_progress DROP COLUMN revision;
