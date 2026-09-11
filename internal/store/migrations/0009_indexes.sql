-- +goose Up
CREATE INDEX idx_editions_work ON editions(work_id, season_num, episode_num);
CREATE INDEX idx_files_edition ON files(edition_id, seq);
CREATE INDEX idx_files_hash ON files(hash) WHERE hash IS NOT NULL;

-- +goose Down
DROP INDEX idx_files_hash;
DROP INDEX idx_files_edition;
DROP INDEX idx_editions_work;
