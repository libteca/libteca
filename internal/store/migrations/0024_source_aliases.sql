-- +goose Up
CREATE TABLE edition_source_aliases (
 source_library_id INTEGER NOT NULL REFERENCES libraries(id),
 source_key TEXT NOT NULL,
 edition_id INTEGER NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 PRIMARY KEY(source_library_id,source_key)
);
INSERT INTO edition_source_aliases SELECT source_library_id,source_key,id FROM editions WHERE source_library_id IS NOT NULL AND source_key IS NOT NULL;
-- +goose Down
DROP TABLE edition_source_aliases;
