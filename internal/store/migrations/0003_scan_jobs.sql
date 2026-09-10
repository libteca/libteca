-- +goose Up

CREATE TABLE scan_jobs (
  id INTEGER PRIMARY KEY,
  library_id INTEGER NOT NULL REFERENCES libraries(id),
  status TEXT NOT NULL CHECK (status IN ('running','done','error')),
  error TEXT,
  files_seen INTEGER NOT NULL DEFAULT 0,
  files_probed INTEGER NOT NULL DEFAULT 0,
  files_added INTEGER NOT NULL DEFAULT 0,
  files_updated INTEGER NOT NULL DEFAULT 0,
  works_changed INTEGER NOT NULL DEFAULT 0,
  started_at INTEGER NOT NULL,
  finished_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE INDEX idx_scan_jobs_library ON scan_jobs(library_id, id);

-- +goose Down

DROP TABLE scan_jobs;
