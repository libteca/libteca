package mediafs

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func OpenWithin(root, path string) (*os.File, fs.FileInfo, error) {
	f, err := Open(root, path)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, fi, nil
}

// Open resolves path against an os.Root and returns the opened regular
// file: processors receive this descriptor instead of the pathname, so a
// swapped symlink or a demuxer-chased secondary resource cannot escape the
// library (audit F03).
func Open(root, path string) (*os.File, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("media path outside library root")
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.Open(rel)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("media is not a regular file")
	}
	return f, nil
}
