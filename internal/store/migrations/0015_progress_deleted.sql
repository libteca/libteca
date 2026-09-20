-- +goose Up
-- Deletion lineage: progress rows are logically deleted (tombstoned)
-- instead of removed, so a recreated row can never collide with a stale
-- base revision captured before the delete (the ABA problem). Conditional
-- writes clear the tombstone; list/discovery readers skip tombstoned rows
-- while the single-row reader still reports their revision so a fresh
-- client can restart from a known base.
ALTER TABLE progress ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1));

-- +goose Down
ALTER TABLE progress DROP COLUMN deleted;
