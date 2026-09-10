package scan

import (
	"github.com/libteca/libteca/internal/store"
)

// fileUnchanged reports whether the store holds a live (missing = 0) row for
// path with matching size and mtime, so the scanner can skip probing it
// (SPEC §3 rule 7). Lookup errors fall back to re-probe.
func fileUnchanged(db *store.DB, path string, size, mtime int64) bool {
	sz, mt, ok, err := db.FileStatByPath(path)
	return err == nil && ok && sz == size && mt == mtime
}
