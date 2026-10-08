-- +goose Up
ALTER TABLE files ADD COLUMN source_library_id INTEGER REFERENCES libraries(id);
ALTER TABLE editions ADD COLUMN source_library_id INTEGER REFERENCES libraries(id);
ALTER TABLE editions ADD COLUMN source_key TEXT;
CREATE UNIQUE INDEX edition_source_identity ON editions(source_library_id,source_key) WHERE source_key IS NOT NULL;
UPDATE files SET source_library_id=(SELECT w.library_id FROM editions e JOIN works w ON w.id=e.work_id WHERE e.id=files.edition_id) WHERE edition_id IS NOT NULL;
-- +goose StatementBegin
CREATE TRIGGER file_source_insert AFTER INSERT ON files WHEN NEW.edition_id IS NOT NULL AND NEW.source_library_id IS NULL BEGIN
 UPDATE files SET source_library_id=(SELECT coalesce(e.source_library_id,w.library_id) FROM editions e JOIN works w ON w.id=e.work_id WHERE e.id=NEW.edition_id) WHERE id=NEW.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER file_source_generation AFTER UPDATE OF source_library_id ON files WHEN OLD.source_library_id IS NOT NEW.source_library_id AND NEW.edition_id IS NOT NULL BEGIN
 UPDATE editions SET timeline_generation=timeline_generation+1 WHERE id=NEW.edition_id;
END;
-- +goose StatementEnd

-- +goose Down
CREATE TEMP TABLE source_downgrade_guard(value INTEGER CHECK(value=1));
INSERT INTO source_downgrade_guard SELECT 0 WHERE EXISTS(SELECT 1 FROM files f JOIN editions e ON e.id=f.edition_id JOIN works w ON w.id=e.work_id WHERE f.source_library_id IS NOT w.library_id);
DROP TABLE source_downgrade_guard;
DROP TRIGGER file_source_generation;
DROP TRIGGER file_source_insert;
DROP INDEX edition_source_identity;
ALTER TABLE editions DROP COLUMN source_key;
ALTER TABLE editions DROP COLUMN source_library_id;
ALTER TABLE files DROP COLUMN source_library_id;
