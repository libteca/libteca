-- +goose Up
ALTER TABLE progress ADD COLUMN media_generation TEXT;
ALTER TABLE progress ADD COLUMN media_file_identity TEXT;
ALTER TABLE progress ADD COLUMN media_operation_id TEXT;
ALTER TABLE podcast_episode_progress ADD COLUMN media_generation TEXT;
ALTER TABLE podcast_episode_progress ADD COLUMN media_file_identity TEXT;
ALTER TABLE podcast_episode_progress ADD COLUMN media_operation_id TEXT;
-- +goose StatementBegin
CREATE TRIGGER progress_media_provenance AFTER UPDATE OF revision ON progress
WHEN old.media_operation_id IS new.media_operation_id
BEGIN UPDATE progress SET media_generation=NULL,media_file_identity=NULL,media_operation_id=NULL WHERE user_id=new.user_id AND edition_id=new.edition_id; END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER podcast_progress_media_provenance AFTER UPDATE OF revision ON podcast_episode_progress
WHEN old.media_operation_id IS new.media_operation_id
BEGIN UPDATE podcast_episode_progress SET media_generation=NULL,media_file_identity=NULL,media_operation_id=NULL WHERE user_id=new.user_id AND episode_id=new.episode_id; END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER podcast_progress_media_provenance;
DROP TRIGGER progress_media_provenance;
ALTER TABLE podcast_episode_progress DROP COLUMN media_operation_id;
ALTER TABLE podcast_episode_progress DROP COLUMN media_file_identity;
ALTER TABLE podcast_episode_progress DROP COLUMN media_generation;
ALTER TABLE progress DROP COLUMN media_operation_id;
ALTER TABLE progress DROP COLUMN media_file_identity;
ALTER TABLE progress DROP COLUMN media_generation;
