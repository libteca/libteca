-- +goose Up
ALTER TABLE editions ADD COLUMN timeline_generation INTEGER NOT NULL DEFAULT 1;
-- +goose StatementBegin
CREATE TRIGGER timeline_file_insert AFTER INSERT ON files BEGIN
 UPDATE editions SET timeline_generation=timeline_generation+1 WHERE id=NEW.edition_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER timeline_file_delete AFTER DELETE ON files BEGIN
 UPDATE editions SET timeline_generation=timeline_generation+1 WHERE id=OLD.edition_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER timeline_file_update AFTER UPDATE ON files
WHEN OLD.edition_id IS NOT NEW.edition_id OR OLD.seq IS NOT NEW.seq OR OLD.size_bytes IS NOT NEW.size_bytes OR OLD.mtime_ns IS NOT NEW.mtime_ns OR OLD.hash IS NOT NEW.hash OR OLD.sha256 IS NOT NEW.sha256 OR OLD.duration_secs IS NOT NEW.duration_secs OR OLD.missing IS NOT NEW.missing OR OLD.codec IS NOT NEW.codec OR OLD.video_codec IS NOT NEW.video_codec OR OLD.chapters IS NOT NEW.chapters
BEGIN
 UPDATE editions SET timeline_generation=timeline_generation+1 WHERE id IN (OLD.edition_id,NEW.edition_id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER timeline_edition_update AFTER UPDATE OF duration_secs ON editions
WHEN OLD.duration_secs IS NOT NEW.duration_secs BEGIN
 UPDATE editions SET timeline_generation=timeline_generation+1 WHERE id=NEW.id;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER timeline_edition_update;
DROP TRIGGER timeline_file_update;
DROP TRIGGER timeline_file_delete;
DROP TRIGGER timeline_file_insert;
ALTER TABLE editions DROP COLUMN timeline_generation;
