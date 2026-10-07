-- +goose Up
-- Reset lineage: a logical reset advances a generation counter that
-- ordinary writes never touch, so an operation captured before a reset can
-- be told apart from one based on the current lineage even after the
-- tombstone is conditionally cleared or the row is recreated. Generation 0
-- means the lineage has never been reset; resetting an absent row inserts
-- a tombstone so the fence survives recreation.
ALTER TABLE progress ADD COLUMN reset_generation INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE progress DROP COLUMN reset_generation;
