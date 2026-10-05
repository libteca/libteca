-- +goose Up
-- Content sha256 for ROM files (omilator client contract). Nullable and
-- games-only by construction: the games scanner fills it at scan time and
-- the work payload backfills legacy rows lazily; other library types keep
-- NULL and the field is simply absent from their payloads.
ALTER TABLE files ADD COLUMN sha256 TEXT;

-- +goose Down
ALTER TABLE files DROP COLUMN sha256;
