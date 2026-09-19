-- +goose Up
-- Reader progress revision protocol: every write bumps the row revision;
-- revision-aware clients post the revision their patch was based on and a
-- stale base is rejected with the current server state. Absent revision
-- keeps last-writer-wins semantics for the non-reader faces.
ALTER TABLE progress ADD COLUMN revision INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE progress DROP COLUMN revision;
