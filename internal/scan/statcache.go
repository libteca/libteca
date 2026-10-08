package scan

import (
	"github.com/libteca/libteca/internal/mediafs"
	"github.com/libteca/libteca/internal/store"
)

// fileUnchanged reports whether the store holds a live (missing = 0) row for
func fileUnchanged(db *store.DB, path string, size, mtime, mtimeNs int64) bool {
	sz, mt, mtns, ok, err := db.FileStatByPath(path)
	if err != nil || !ok || sz != size || mt != mtime || mtns != mtimeNs || mtns == 0 {
		return false
	}
	var sha *string
	var root string
	if err := db.QueryRow(`SELECT f.sha256,l.path FROM files f JOIN libraries l ON l.id=f.source_library_id WHERE f.path=?`, path).Scan(&sha, &root); err != nil {
		return false
	}
	if sha == nil || *sha == "" {
		return false
	}
	f, err := mediafs.Open(root, path)
	if err != nil {
		return false
	}
	defer f.Close()
	return SHA256Content(f) == *sha
}
