-- +goose Up

-- Genres move from the settings KV (genres:work/{id} rows, written by the
-- matching pipeline before this column existed) onto works itself. The old
-- KV rows are intentionally left in place: nothing reads them anymore and
-- deleting user data in a migration is not worth the risk.

ALTER TABLE works ADD COLUMN genres TEXT NOT NULL DEFAULT '[]';

UPDATE works SET genres = COALESCE(
  (SELECT s.value FROM settings s WHERE s.key = 'genres:work/' || works.id),
  genres
);

-- Episode descriptions for TV: apply-episodes stores the season's first
-- episode overview here, only when empty.
ALTER TABLE editions ADD COLUMN description TEXT;

-- +goose Down

ALTER TABLE editions DROP COLUMN description;
ALTER TABLE works DROP COLUMN genres;
