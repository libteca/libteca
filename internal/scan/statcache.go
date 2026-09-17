package scan

import (
	"github.com/libteca/libteca/internal/store"
)

// fileUnchanged reports whether the store holds a live (missing = 0) row for
func fileUnchanged(db *store.DB, path string, size, mtime, mtimeNs int64) bool {
	sz, mt, mtns, ok, err := db.FileStatByPath(path)
	return err == nil && ok && sz == size && mt == mtime && mtns == mtimeNs && mtns != 0
}
