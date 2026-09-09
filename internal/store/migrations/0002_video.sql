-- +goose Up

ALTER TABLE editions ADD COLUMN season_num INTEGER;
ALTER TABLE editions ADD COLUMN episode_num INTEGER;
ALTER TABLE files ADD COLUMN video_codec TEXT;
ALTER TABLE files ADD COLUMN width INTEGER;
ALTER TABLE files ADD COLUMN height INTEGER;

-- +goose Down

ALTER TABLE editions DROP COLUMN season_num;
ALTER TABLE editions DROP COLUMN episode_num;
ALTER TABLE files DROP COLUMN video_codec;
ALTER TABLE files DROP COLUMN width;
ALTER TABLE files DROP COLUMN height;
