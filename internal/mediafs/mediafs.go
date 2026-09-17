package mediafs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

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
