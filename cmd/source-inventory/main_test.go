package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestCommandRequiresExplicitSnapshotAndStrictMapping(t *testing.T) {
	for _, args := range [][]string{nil, {"unrequested.db"}, {"--snapshot", "missing.db", "extra"}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 2 || out.Len() != 0 {
			t.Fatalf("unexpected CLI result: %d %s %s", code, &out, &stderr)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--snapshot", path}, &out, &stderr); code != 0 {
		t.Fatalf("command failed: %d %s", code, &stderr)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"read_only": true`)) {
		t.Fatal("missing read-only report")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("command mutated snapshot bytes")
	}
	for _, content := range []string{`[{"library_id":1,"disposable_root":"/tmp","unexpected":true}]`, `[] {}`} {
		mapping := filepath.Join(dir, "map.json")
		if err := os.WriteFile(mapping, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		stderr.Reset()
		if code := run(context.Background(), []string{"--snapshot", path, "--root-map", mapping}, &out, &stderr); code != 1 || out.Len() != 0 {
			t.Fatalf("accepted malformed map: %d %s", code, &out)
		}
	}
}
