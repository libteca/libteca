-- +goose Up
-- Token values at rest converge to sha256 digests (audit F09): the Go-side
-- startup rewrite in store.Open converts legacy plaintext rows in ONE
-- transaction; this marker column keeps the rewrite idempotent (a digest
-- row can never be mistaken for plaintext and re-hashed).
ALTER TABLE tokens ADD COLUMN digested INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE tokens DROP COLUMN digested;
