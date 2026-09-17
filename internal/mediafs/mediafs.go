package mediafs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// OpenWithin opens path confined to root: the relative path must stay local
// to root and the open goes through os.OpenRoot, so an escaping symlink
// recorded in the database (or racing replacement of a path component) can
// never reach bytes outside the library. Only regular files are returned.
func OpenWithin(root, path string) (*os.File, fs.FileInfo, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return nil, nil, fmt.Errorf("media path outside library root")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	f, err := r.Open(rel)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("media is not a regular file")
	}
	return f, fi, nil
}
