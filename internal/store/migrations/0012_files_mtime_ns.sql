-- +goose Up
-- Sub-second file versioning: comparing integer-second mtimes missed
-- same-second rewrites. Legacy rows carry 0 and force exactly one re-probe.
ALTER TABLE files ADD COLUMN mtime_ns INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE files DROP COLUMN mtime_ns;
