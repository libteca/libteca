package scan

import (
	"github.com/libteca/libteca/internal/store"
)

// fileUnchanged reports whether the store holds a live (missing = 0) row for
// path with matching size and mtime (seconds AND nanoseconds - legacy rows
// carry ns=0 and re-probe once), so the scanner can skip probing it (SPEC §3
// rule 7). Lookup errors fall back to re-probe.
func fileUnchanged(db *store.DB, path string, size, mtime, mtimeNs int64) bool {
	sz, mt, mtns, ok, err := db.FileStatByPath(path)
	return err == nil && ok && sz == size && mt == mtime && mtns == mtimeNs && mtns != 0
}
