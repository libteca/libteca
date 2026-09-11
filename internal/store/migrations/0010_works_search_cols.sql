-- +goose Up

ALTER TABLE works ADD COLUMN title_l TEXT;
ALTER TABLE works ADD COLUMN author_l TEXT;

-- +goose Down

ALTER TABLE works DROP COLUMN author_l;
ALTER TABLE works DROP COLUMN title_l;
