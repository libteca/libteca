package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestReadonlyReviewRejectAmbiguousRootMap(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "snapshot.db")
	db, err := store.Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO libraries VALUES (1,'Synthetic A','books','/recorded/a',1),(2,'Synthetic B','books','/recorded/b',1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	rootJSON, err := json.Marshal(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{
		`null`,
		`[{"library_id":1,"library_id":2,"disposable_root":` + string(rootJSON) + `}]`,
		`[{"library_id":1,"disposable_root":"/nonexistent/earlier-root","disposable_root":` + string(rootJSON) + `}]`,
	} {
		t.Run(content, func(t *testing.T) {
			mapping := filepath.Join(dir, "map.json")
			if err := os.WriteFile(mapping, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			if code := run(context.Background(), []string{"--snapshot", snapshot, "--root-map", mapping}, &out, &stderr); code != 1 || out.Len() != 0 {
				t.Fatalf("accepted ambiguous mapping: exit=%d output=%s stderr=%s", code, &out, &stderr)
			}
		})
	}
}

func TestReadonlyReviewRejectMalformedRootMapStructure(t *testing.T) {
	for _, content := range []string{
		``, ` `, `null`, `{}`, `[`, `[null]`, `[[]]`, `[{}]`,
		`[{"library_id":1}]`, `[{"disposable_root":"/copied"}]`,
		`[{"library_id":null,"disposable_root":"/copied"}]`,
		`[{"library_id":1,"disposable_root":null}]`,
		`[{"library_id":0,"disposable_root":"/copied"}]`,
		`[{"library_id":1,"disposable_root":""}]`,
		`[{"library_id":1.5,"disposable_root":"/copied"}]`,
		`[{"LIBRARY_ID":1,"disposable_root":"/copied"}]`,
		`[{"library_id":1,"disposable_root":"/copied"}`,
		`[{"library_id":1,"disposable_root":"/copied"]`,
		`[{"library_id":1,"disposable_root":"/copied"},]`,
		`[] null`, `[] []`, `[] trailing`,
	} {
		t.Run(content, func(t *testing.T) {
			if mappings, err := decodeRootMappings(strings.NewReader(content)); err == nil || mappings != nil {
				t.Fatalf("malformed map accepted: input=%q mappings=%+v err=%v", content, mappings, err)
			}
		})
	}
	for _, content := range []string{`[]`, ` [{"library_id":1,"disposable_root":"/copied"}] `} {
		if mappings, err := decodeRootMappings(strings.NewReader(content)); err != nil || mappings == nil {
			t.Fatalf("valid map rejected: input=%q mappings=%+v err=%v", content, mappings, err)
		}
	}
}
